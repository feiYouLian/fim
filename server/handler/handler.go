package handler

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/ivpusic/grpool"
	"github.com/klintcheng/fim/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

type Dispatcher interface {
	To(gateway ksuid.KSUID, pkg *wire.RelayPacket) error
}

type HandlerConfig struct {
	PoolLogin   int
	PoolLogout  int
	PoolChat    int
	PoolChatAck int
}

// MessageHandler MessageHandler
type MessageHandler struct {
	ID               ksuid.KSUID
	conf             HandlerConfig
	in               chan *wire.RelayPacket
	dr               Dispatcher
	state            int32
	log              *log.Entry
	messageQueueSize *prometheus.GaugeVec
}

type handlerFunc func(ctx context.Context, mh *MessageHandler, msg *wire.RelayPacket) error

type handleSetting struct {
	pool   int //indicates how many goroutine are used to running the handler
	handle handlerFunc
}

//NewMessageHandler new MessageHandler
func NewMessageHandler(sid ksuid.KSUID, dr Dispatcher) (*MessageHandler, error) {
	h := &MessageHandler{
		ID:               sid,
		in:               make(chan *wire.RelayPacket, 100),
		dr:               dr,
		state:            0,
		log:              log.WithField("ID", sid),
		messageQueueSize: messageQueueSize,
	}

	return h, nil
}

// Send Send
func (h *MessageHandler) Send(pkg *wire.RelayPacket) error {
	h.in <- pkg
	return nil
}

//forward a message to gateway through hub
func (h *MessageHandler) forward(gatewayID interface{}, message *wire.Message, receivers ...interface{}) {
	if gatewayID == "" {
		log.Error("forward to null gateway")
	}
	if len(receivers) == 0 {
		return
	}
	relayMsg := wire.NewEmptyRelayMessage(h.ID)
	relayMsg.Receivers = make([]ksuid.KSUID, len(receivers))
	relayMsg.Payload = message

	for i, recv := range receivers {
		if str, ok := recv.(string); ok {
			relayMsg.Receivers[i], _ = ksuid.Parse(str)
		} else if id, ok := recv.(ksuid.KSUID); ok {
			relayMsg.Receivers[i] = id
		}
	}
	log.WithFields(log.Fields{
		"in ":     gatewayID,
		"Header:": relayMsg.Payload.Header,
	}).Debugln("forward to ", receivers)

	var gid ksuid.KSUID
	if id, ok := gatewayID.(string); ok {
		gid, _ = ksuid.Parse(id)
	} else if id, ok := gatewayID.(ksuid.KSUID); ok {
		gid = id
	}

	h.dr.To(gid, relayMsg)
}

// Start Start
func (h *MessageHandler) Start() {
	if !atomic.CompareAndSwapInt32(&h.state, 0, 1) {
		return
	}
	go h.handleInMessage()
}

func (h *MessageHandler) handleInMessage() {
	var settings = map[wire.PID]handleSetting{
		wire.PIDLogin:       {h.conf.PoolLogin, loginHandler},
		wire.PIDLogout:      {h.conf.PoolLogout, logoutHandler},
		wire.PIDChat:        {h.conf.PoolChat, chatHandler},
		wire.PIDChatPushAck: {h.conf.PoolChatAck, chatPushAckHandler},
	}

	// initialize protocol handler with a limited goroutine pool
	handlePools := make(map[wire.PID]*grpool.Pool, len(settings))
	for pid, set := range settings {
		handlePools[pid] = grpool.NewPool(set.pool, set.pool*10)
	}

	for msg := range h.in {
		pid := msg.Payload.Header.GetPID()
		handlePool, has := handlePools[pid]
		if !has {
			log.WithField("PID", pid).Debug("protocol has no handler")
			continue
		}
		messageQueueSize.WithLabelValues(h.ID.String(), pid.String()).Inc()
		rmsg := msg // get a copy
		handle := settings[pid].handle
		handlePool.JobQueue <- func() {
			h.poolFunc(handle, rmsg)
		}
	}
}

// entrance of handler
func (h *MessageHandler) poolFunc(handle handlerFunc, msg *wire.RelayPacket) {
	header := msg.Payload.Header
	sid := h.ID.String()
	pid := header.GetPID().String()
	defer func() {
		messageQueueSize.WithLabelValues(sid, pid).Dec()

		if r := recover(); r != nil {
			log.WithField("func", "poolFunc").Error(r)
		}
	}()

	t1 := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := handle(ctx, h, msg); err != nil {
		log.Error(err)
	}
	cost := time.Since(t1)
	handleDurationSeconds.WithLabelValues(sid, pid).Observe(cost.Seconds())
	handleDurationSecondsSummary.WithLabelValues(sid, pid).Observe(cost.Seconds())
	log.WithFields(log.Fields{
		"from": msg.Uuid,
		"cost": cost,
	}).Debugln("handle", header)
}

// Close Close
func (h *MessageHandler) Close(ctx context.Context) {

}
