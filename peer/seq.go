package peer

import (
	"math"
	"sync/atomic"
)

// Sequence Sequence
type sequence struct {
	num uint32
}

// Next return Next Seq id
func (s *sequence) Next() uint16 {
	next := atomic.AddUint32(&s.num, 1)
	if next == math.MaxUint16 {
		if atomic.CompareAndSwapUint32(&s.num, next, 1) {
			return 1
		}
		return s.Next()
	}
	return uint16(next)
}

// Seq Seq
var Seq = sequence{num: 1}
