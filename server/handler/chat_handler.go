package handler

import (
	"context"

	"github.com/klintcheng/fim/wire"
)

// Role for group
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
	RoleOwner = "owner"
)

// Mute Level in group
const (
	MuteNone = 0
	MuteUser = 1
	MuteAll  = 2
)

const (
	hitLevelNone = iota
	// hitLevelFiltered
	hitLevelBlocked
)

// Msg Type
const (
	MsgText     = 1  // 文本
	MsgImage    = 2  // 图片
	MsgVoice    = 3  // 语音
	MsgVideo    = 4  //视频
	MsgFace     = 5  //表情
	MsgFile     = 6  //文件
	MsgLocation = 7  //位置消息
	MsgEvent    = 50 //事件
	MsgNotice   = 51 //通知
	MsgRecall   = 52 //消息撤回
	MsgDelete   = 53 //消息删除 group
)

//chat handle chat message
func chatHandler(ctx context.Context, h *MessageHandler, msg *wire.RelayPacket) error {

	return nil
}

//chat handle group chat message
func groupHandler(ctx context.Context, h *MessageHandler, msg *wire.RelayPacket) error {

	return nil
}

//chatPushAckHandler
func chatPushAckHandler(ctx context.Context, h *MessageHandler, msg *wire.RelayPacket) error {

	return nil
}
