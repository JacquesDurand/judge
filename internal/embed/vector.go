package embed

import (
	"strconv"
	"strings"
)

// VectorLiteral renders a float slice as pgvector's text input format, e.g.
// "[0.12,-0.03,...]". Cast it with `$1::vector` in SQL, both when storing
// embeddings and when querying with the distance operators.
func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
