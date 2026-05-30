package script

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func bigBody() []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"gpt-4o","messages":[`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf(`{"role":"user","content":"message number %d with some padding text"}`, i))
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

func benchRun(b *testing.B, code string, body []byte) {
	e := engineWith(time.Second, mustCompile(b, "s", 0, code))
	in := HookInput{Hook: HookRequest, Model: "m", Body: body}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.Run(in)
	}
}

func BenchmarkRun_HeaderOnly_SmallBody(b *testing.B) {
	benchRun(b, `function onRequest(ctx){ ctx.setHeader("X-A","1") }`, []byte(`{"model":"m"}`))
}

func BenchmarkRun_HeaderOnly_BigBody(b *testing.B) {
	benchRun(b, `function onRequest(ctx){ ctx.setHeader("X-A","1") }`, bigBody())
}

func BenchmarkRun_BodyRewrite_BigBody(b *testing.B) {
	benchRun(b, `function onRequest(ctx){ ctx.body.temperature = 0.5 }`, bigBody())
}
