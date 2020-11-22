package peer

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

func TestNewClient(t *testing.T) {
	lst := &ClientListener{
		OnMsg:        make(chan *ClientMessage, 1),
		OnDisconnect: make(chan *WsClient, 1),
	}
	pConf := &ClientConf{
		PongWait:   10 * time.Second,
		RemotePing: true,
		Rate:       1000,
	}
	quit := NewEvent()
	quit2 := NewEvent()
	go func() {
		_ = http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, _, _, err := ws.UpgradeHTTP(r, w)
			if err != nil {
				// handle error
				log.Error(err)
				return
			}
			client := NewClient(conn, lst, pConf, &ClientAttrs{})
			if err = client.Start(); err != nil {
				log.Error(err)
				return
			}

		}))
	}()
	total := 20
	go func() {
		count := 0
		for {
			select {
			case <-lst.OnMsg:
				count++
				log.Info("recv ", count)
				if count == total {
					quit.Fire()
				}
				time.Sleep(time.Millisecond * 10)
			case c := <-lst.OnDisconnect:
				log.Info("disconnect", c.ID)
				quit2.Fire()
			}
		}
	}()

	conn, _, _, err := ws.DefaultDialer.Dial(context.Background(), "ws://localhost:8080")
	if err != nil {
		// handle error
		log.Error(err)
		return
	}

	msg := bytes.Repeat([]byte{'a'}, 100)
	log.Info(conn.RemoteAddr())
	for i := 0; i < total; i++ {
		err := wsutil.WriteClientBinary(conn, msg)
		if err != nil {
			// handle error
			log.Error(err)
			break
		}
		time.Sleep(time.Millisecond * 10)
	}

	<-quit.Done()
	conn.Close()
	log.Info("conn close..")
	<-quit2.Done()
}
