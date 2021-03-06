package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/klintcheng/fim/gateway/wss"
	"github.com/klintcheng/fim/lgcluster"
	"github.com/klintcheng/fim/namesrv/client"
	"github.com/sirupsen/logrus"

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

func main() {
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

	h, err := wss.NewGateway(id, wss.HubConf{
		JwtAppkey: viper.GetString("appkey"),
		JwtSecret: viper.GetString("secret"),
		Listen:    viper.GetString("listen"),
		SSL:       viper.GetBool("ssl"),
	}, proxy)
	if err != nil {
		panic(err)
	}

	if err := h.Start(); err != nil {
		panic(err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	logrus.Info("Shutdown Server ...", <-quit)
}
