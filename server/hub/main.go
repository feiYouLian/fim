package hub

import (
	_ "expvar"
	_ "net/http/pprof"

	"runtime"
)

// Main enter system
func Main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

}
