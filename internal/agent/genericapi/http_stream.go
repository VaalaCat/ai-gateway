package genericapi

import (
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"golang.org/x/net/http/httpguts"
)

const localHTTPResponseBufferSize = 32 * 1024

var (
	ErrInvalidUpstreamResponse = errors.New("invalid upstream response")
	localHTTPResponseBuffers   = sync.Pool{New: func() any {
		buffer := make([]byte, localHTTPResponseBufferSize)
		return &buffer
	}}
)

// HTTPStream copies one upstream HTTP response to the local client without
// retaining its body. The zero value is ready to use.
type HTTPStream struct {
	startedAt time.Time
}

func newHTTPStream(startedAt time.Time) HTTPStream {
	return HTTPStream{startedAt: startedAt}
}

func (s HTTPStream) Copy(client http.ResponseWriter, upstream *http.Response) (apiattempt.APIExecutionResult, error) {
	result := apiattempt.APIExecutionResult{}
	if client == nil || upstream == nil || upstream.StatusCode < 100 || upstream.StatusCode > 999 {
		return result, ErrInvalidUpstreamResponse
	}

	copySafeResponseHeaders(client.Header(), upstream.Header)
	trailerNames := safeResponseTrailerNames(upstream.Trailer)
	declareResponseTrailers(client.Header(), trailerNames)
	result.UpstreamStatus = upstream.StatusCode
	commitHTTPResponse(client, upstream.StatusCode)
	result.FirstByteMs = elapsedMilliseconds(s.startedAt)

	if upstream.Body == nil || upstream.Body == http.NoBody {
		copyFinalResponseTrailers(client.Header(), upstream.Trailer, trailerNames)
		return result, nil
	}
	buffer := localHTTPResponseBuffers.Get().(*[]byte)
	defer localHTTPResponseBuffers.Put(buffer)
	var err error
	result.ResponseBytes, err = io.CopyBuffer(flushingResponseWriter{ResponseWriter: client}, upstream.Body, *buffer)
	if result.ResponseBytes < 0 {
		result.ResponseBytes = 0
	}
	if err != nil {
		return result, err
	}
	copyFinalResponseTrailers(client.Header(), upstream.Trailer, trailerNames)
	return result, nil
}

func commitHTTPResponse(client http.ResponseWriter, status int) {
	client.WriteHeader(status)
	if committer, ok := client.(interface{ WriteHeaderNow() }); ok {
		committer.WriteHeaderNow()
	}
}

type flushingResponseWriter struct {
	http.ResponseWriter
}

type clientResponseWriteError struct{ cause error }

func (failure *clientResponseWriteError) Error() string { return failure.cause.Error() }
func (failure *clientResponseWriteError) Unwrap() error { return failure.cause }

func (w flushingResponseWriter) Write(value []byte) (int, error) {
	count, err := w.ResponseWriter.Write(value)
	if err != nil {
		return count, &clientResponseWriteError{cause: err}
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return count, nil
}

func copySafeResponseHeaders(target, source http.Header) {
	connectionNames := connectionHeaderNames(source)
	for name, values := range source {
		if unsafeResponseHeader(name, connectionNames) || !validHeaderValues(values) {
			continue
		}
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

func unsafeResponseHeader(name string, connectionNames map[string]struct{}) bool {
	if !httpguts.ValidHeaderFieldName(name) {
		return true
	}
	lower := strings.ToLower(name)
	if _, hopByHop := hopByHopRequestHeaders[lower]; hopByHop {
		return true
	}
	if _, namedByConnection := connectionNames[lower]; namedByConnection {
		return true
	}
	return lower == "authorization" || lower == "forwarded" || gatewayInternalHeader(lower)
}

func safeResponseTrailerNames(trailers http.Header) []string {
	names := make(map[string]struct{}, len(trailers))
	for name := range trailers {
		if addTrailerName(names, name) != nil {
			continue
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func declareResponseTrailers(header http.Header, names []string) {
	for _, name := range names {
		header.Add("Trailer", name)
	}
}

func copyFinalResponseTrailers(target, source http.Header, names []string) {
	for _, name := range names {
		values := source.Values(name)
		if !validHeaderValues(values) {
			continue
		}
		target[name] = append([]string(nil), values...)
	}
}

func elapsedMilliseconds(startedAt time.Time) int {
	if startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(startedAt).Milliseconds()
	if elapsed <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if elapsed > maxInt {
		return int(maxInt)
	}
	return int(elapsed)
}
