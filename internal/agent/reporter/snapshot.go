// internal/agent/reporter/snapshot.go
package reporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// retrySnapshotItem 是快照信封里旁路重试队列的一条记录——只落 Restore 之后还需要
// 的字段(entry/attempts/degrade),bytes 恢复时靠 entrySize 重算,nextAt 恢复时靠
// retryBackoff(attempts) 重算(不落零值远古时间,见 Restore)。
type retrySnapshotItem struct {
	Entry    protocol.UsageLogEntry `json:"entry"`
	Attempts int                    `json:"attempts"`
	Degrade  int                    `json:"degrade"`
}

// backlogSnapshot 是磁盘快照的信封;Version 用于向前兼容判断(非 1 一律当损坏处理)。
type backlogSnapshot struct {
	Version  int                      `json:"version"`
	SavedAt  int64                    `json:"saved_at"`
	Store    []protocol.UsageLogEntry `json:"store"`
	Retry    []retrySnapshotItem      `json:"retry"`
	Inflight []protocol.UsageLogEntry `json:"inflight"`
}

// Snapshotter 把 Store/旁路重试队列/旁路在飞条目周期性落盘成一个 gzip 文件,agent
// 进程被杀(kill -9、OOM、崩溃)时,原本只活在内存里的待投递用量数据不再随进程
// 消失——启动时 Restore() 把它们捞回来,交给 master 的 request_id 去重吸收重复。
type Snapshotter struct {
	Store    PendingUsageStore
	Uploader *UsageUploader
	Path     string // <凭据目录>/usage_backlog.snapshot.gz
	Logger   *zap.Logger
}

// Run:60s ticker,指纹(store.Len/Bytes + retry.Len/totalBytes + inflight 数)变了才写;
// ctx.Done 后等 Uploader.DrainDone()(上限 6s,略大于 drainOnShutdown 的 5s 窗口)再做
// 最终 WriteNow——drain 排空多少算多少,快照兜住剩下的,memory-at-exit 损耗就此消失。
func (s *Snapshotter) Run(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	lastSig := ""
	for {
		select {
		case <-ctx.Done():
			<-s.Uploader.FinalDrainDone()
			if err := s.WriteNow(); err != nil {
				s.Logger.Error("final backlog snapshot failed, pending usage lost on exit", zap.Error(err))
			}
			return
		case <-ticker.C:
			sig := s.signature()
			if sig == lastSig {
				continue
			}
			if err := s.WriteNow(); err != nil {
				s.Logger.Error("backlog snapshot failed", zap.Error(err))
				continue
			}
			lastSig = sig
		}
	}
}

func (s *Snapshotter) signature() string {
	return fmt.Sprintf("%d/%d|%d/%d|%d",
		s.Store.Len(), s.Store.Bytes(),
		s.Uploader.retry.Len(), s.Uploader.retry.totalBytes(),
		s.Uploader.InflightCount())
}

// WriteNow 原子写(tmp+rename);store/retry/inflight 全空时删掉旧文件而不是写一个
// 空信封——避免 Restore 每次启动都要多解一层 gzip/JSON 才发现什么都没有。
func (s *Snapshotter) WriteNow() error {
	snap := backlogSnapshot{Version: 1, SavedAt: time.Now().Unix(),
		Store:    s.Store.PeekBatch(1 << 30), // 全量拷贝
		Inflight: s.Uploader.inflightEntries()}
	for _, it := range s.Uploader.retry.snapshotAll() {
		snap.Retry = append(snap.Retry, retrySnapshotItem{Entry: it.entry, Attempts: it.attempts, Degrade: it.degrade})
	}
	if len(snap.Store) == 0 && len(snap.Retry) == 0 && len(snap.Inflight) == 0 {
		if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(snap); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	// fsync 后再 rename:防主机断电时新文件内容未落盘而旧文件已被顶掉(spec §6)
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Restore:恢复后**不删**快照文件——"恢复成功但立刻崩"的窗口里数据还在盘上,
// 下一个 60s 周期自然覆盖;期间与崩溃间隙的重复上传由 master request_id 去重吸收。
func (s *Snapshotter) Restore() (int, error) {
	raw, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	quarantine := func(stage string, cause error) {
		s.Logger.Error("backlog snapshot corrupt, quarantined",
			zap.String("stage", stage), zap.Error(cause))
		os.Rename(s.Path, s.Path+".corrupt")
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		quarantine("gzip", err)
		return 0, nil
	}
	var snap backlogSnapshot
	if err := json.NewDecoder(zr).Decode(&snap); err != nil || snap.Version != 1 {
		quarantine("decode", err)
		return 0, nil
	}
	s.Store.Append(snap.Store)
	s.Store.Append(snap.Inflight) // 在飞状态未知,按未投递处理;重复由 master 去重吸收
	now := time.Now()
	for _, it := range snap.Retry {
		s.Uploader.retry.pushItem(retryItem{entry: it.Entry, attempts: it.Attempts,
			degrade: it.Degrade, bytes: entrySize(it.Entry),
			nextAt: now.Add(s.Uploader.retryBackoff(it.Attempts))})
	}
	n := len(snap.Store) + len(snap.Inflight) + len(snap.Retry)
	s.Logger.Info("restored pending usage backlog from snapshot",
		zap.Int("entries", n), zap.Int64("saved_at", snap.SavedAt))
	return n, nil
}
