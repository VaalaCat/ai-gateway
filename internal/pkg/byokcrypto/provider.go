package byokcrypto

// Provider 暴露当前进程持有的 BYOK *Cipher。
//
// 引入 Provider 是为了把 BYOK 这个 master-only 关注点从 app.Application
// god-interface 里抽出去：consumer（private_channel.Handler、sync FetchRegistry
// 里的 privateChannelsVisibleFetchHandler）只依赖 Provider，agent 进程不再
// 被迫实现 GetBYOKCipher / SetBYOKCipher 返回 nil。
//
// 当前只有 staticProvider 一种实现（构造时绑定一个 *Cipher 并恒返之）；
// 未来若要支持 KEK 热轮转，再在这里加新实现。
type Provider interface {
	// GetCipher 返回当前可用的 *Cipher；BYOK 未配置时返回 nil，
	// 调用方应对 nil 做防御性 error 返回，禁止解引用。
	GetCipher() *Cipher
}

// staticProvider 是构造时绑定单个 *Cipher 的最简实现。
type staticProvider struct {
	c *Cipher
}

// NewStaticProvider 把一个 *Cipher（可为 nil）包装成 Provider。
func NewStaticProvider(c *Cipher) Provider {
	return staticProvider{c: c}
}

func (p staticProvider) GetCipher() *Cipher { return p.c }
