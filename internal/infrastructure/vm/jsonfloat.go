package vm

import (
	"math"
	"strconv"
)

// AppendJSONFloat appends v in the /api/v1/export JSON format: NaN as null, +Inf as "Infinity",
// -Inf as "-Infinity", finite values as plain JSON numbers. /api/v1/import accepts all of them.
func AppendJSONFloat(dst []byte, v float64) []byte {
	switch {
	case math.IsNaN(v):
		return append(dst, "null"...)
	case math.IsInf(v, 1):
		return append(dst, `"Infinity"`...)
	case math.IsInf(v, -1):
		return append(dst, `"-Infinity"`...)
	default:
		return strconv.AppendFloat(dst, v, 'g', -1, 64)
	}
}
