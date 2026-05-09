package mdrender

import (
	"strings"
	"testing"
)

// fixturePlan500 builds a deterministic ~500-line plan that exercises every
// supported markdown construct: headers, paragraphs, lists, blockquotes,
// fenced code (Go and bash), inline spans, links, and HRs. The shape mirrors
// what the planner actually produces so the benchmark reflects realistic
// per-keystroke render cost.
func fixturePlan500() string {
	var b strings.Builder
	b.WriteString("# Top-Level Plan\n\n")
	b.WriteString("This is a **bold** paragraph with *italic*, `inline code`, and a [link](https://example.com).\n\n")
	b.WriteString("> A blockquote that explains the motivation behind the work below.\n\n")
	b.WriteString("---\n\n")

	for i := 1; i <= 50; i++ {
		b.WriteString("## Section ")
		b.WriteString(itoa(i))
		b.WriteString("\n\n")
		b.WriteString("Description with **bold**, *italic*, and `code` so the inline-span pass has work to do.\n\n")
		b.WriteString("- bullet one\n- bullet two with [a link](https://example.com/x)\n- bullet three\n\n")
		if i%5 == 0 {
			b.WriteString("```go\n")
			b.WriteString("func Example" + itoa(i) + "() error {\n")
			b.WriteString("    return nil\n")
			b.WriteString("}\n")
			b.WriteString("```\n\n")
		} else if i%7 == 0 {
			b.WriteString("```bash\n")
			b.WriteString("echo \"step " + itoa(i) + "\"\n")
			b.WriteString("```\n\n")
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	if n < 0 {
		digits = append(digits, '-')
		n = -n
	}
	start := len(digits)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// reverse the appended digits in-place
	for i, j := start, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

func BenchmarkRenderLines(b *testing.B) {
	plan := fixturePlan500()
	r := New("monokai")
	// Warm caches once so the bench measures steady-state hit cost.
	r.RenderLines(plan, 80)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RenderLines(plan, 80)
	}
}

func BenchmarkRenderLines_ColdCache(b *testing.B) {
	plan := fixturePlan500()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := New("monokai")
		_ = r.RenderLines(plan, 80)
	}
}
