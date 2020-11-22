package hub

import (
	_ "expvar"
	_ "net/http/pprof"

	"runtime"

	"github.com/klintcheng/fim/lgcluster"
	"github.com/klintcheng/fim/namesrv/client"
	"github.com/klintcheng/fim/server/hub"
	"github.com/segmentio/ksuid"
	"github.com/spf13/viper"
)

func Id() (ksuid.KSUID, error) {
	if viper.GetString("id") == "" {
		return ksuid.NewRandom()
	}
	id, err := ksuid.Parse(viper.GetString("id"))
	return id, err
}

// Main enter system
func Main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	id, err := Id()
	if err != nil {
		panic(err)
	}
	namesrv, err := client.NewNamingClient(client.Options{})
	if err != nil {
		panic(err)
	}

	proxy, err := lgcluster.NewLgClusterProxy(id, namesrv, nil)
	if err != nil {
		panic(err)
	}

	lg, err := hub.NewLgServer(id, hub.ServerConfig{
		ListenTCP: viper.GetString("listen"),
	}, proxy)
	if err != nil {
		panic(err)
	}
	lg.Start()

}
