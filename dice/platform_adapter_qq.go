package dice

import (
	"encoding/json"
	"fmt"
	"github.com/fy0/procs"
	"github.com/sacOO7/gowebsocket"
	"math/rand"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type PlatformAdapterQQOnebot struct {
	EndPoint *EndPointInfo `yaml:"-" json:"-"`
	Session  *IMSession    `yaml:"-" json:"-"`

	Socket     *gowebsocket.Socket `yaml:"-" json:"-"`
	ConnectUrl string              `yaml:"connectUrl" json:"connectUrl"` // 连接地址

	UseInPackGoCqhttp                bool           `yaml:"useInPackGoCqhttp" json:"useInPackGoCqhttp"` // 是否使用内置的gocqhttp
	InPackGoCqLastAutoLoginTime      int64          `yaml:"inPackGoCqLastAutoLoginTime" json:"-"`       // 上次自动重新登录的时间
	InPackGoCqHttpProcess            *procs.Process `yaml:"-" json:"-"`
	InPackGoCqHttpLoginSuccess       bool           `yaml:"-" json:"inPackGoCqHttpLoginSuccess"`   // 是否登录成功
	InPackGoCqHttpLoginSucceeded     bool           `yaml:"inPackGoCqHttpLoginSucceeded" json:"-"` // 是否登录成功过
	InPackGoCqHttpRunning            bool           `yaml:"-" json:"inPackGoCqHttpRunning"`        // 是否仍在运行
	InPackGoCqHttpQrcodeReady        bool           `yaml:"-" json:"inPackGoCqHttpQrcodeReady"`    // 二维码已就绪
	InPackGoCqHttpNeedQrCode         bool           `yaml:"-" json:"inPackGoCqHttpNeedQrCode"`     // 是否需要二维码
	InPackGoCqHttpQrcodeData         []byte         `yaml:"-" json:"-"`                            // 二维码数据
	InPackGoCqHttpLoginDeviceLockUrl string         `yaml:"-" json:"inPackGoCqHttpLoginDeviceLockUrl"`
	InPackGoCqHttpLastRestrictedTime int64          `yaml:"inPackGoCqHttpLastRestricted" json:"inPackGoCqHttpLastRestricted"` // 上次风控时间
	InPackGoCqHttpProtocol           int            `yaml:"inPackGoCqHttpProtocol" json:"inPackGoCqHttpProtocol"`
	InPackGoCqHttpPassword           string         `yaml:"inPackGoCqHttpPassword" json:"-"`
	DiceServing                      bool           `yaml:"-"`          // 是否正在连接中
	InPackGoCqHttpDisconnectedCH     chan int       `yaml:"-" json:"-"` // 信号量，用于关闭连接
}

type Sender struct {
	Age      int32  `json:"age"`
	Card     string `json:"card"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"` // owner 群主
	UserId   int64  `json:"user_id"`
}

type MessageQQ struct {
	MessageId     int64   `json:"message_id"`   // QQ信息此类型为int64，频道中为string
	MessageType   string  `json:"message_type"` // Group
	Sender        *Sender `json:"sender"`       // 发送者
	RawMessage    string  `json:"raw_message"`
	Message       string  `json:"message"` // 消息内容
	Time          int64   `json:"time"`    // 发送时间
	MetaEventType string  `json:"meta_event_type"`
	OperatorId    int64   `json:"operator_id"`  // 操作者帐号
	GroupId       int64   `json:"group_id"`     // 群号
	PostType      string  `json:"post_type"`    // 上报类型，如group、notice
	RequestType   string  `json:"request_type"` // 请求类型，如group
	SubType       string  `json:"sub_type"`     // 子类型，如add invite
	Flag          string  `json:"flag"`         // 请求 flag, 在调用处理请求的 API 时需要传入
	NoticeType    string  `json:"notice_type"`
	UserId        int64   `json:"user_id"`
	SelfId        int64   `json:"self_id"`
	Duration      int64   `json:"duration"`
	Comment       string  `json:"comment"`

	Data *struct {
		// 个人信息
		Nickname string `json:"nickname"`
		UserId   int64  `json:"user_id"`

		// 群信息
		GroupId         int64  `json:"group_id"`          // 群号
		GroupCreateTime uint32 `json:"group_create_time"` // 群号
		MemberCount     int64  `json:"member_count"`
		GroupName       string `json:"group_name"`
		MaxMemberCount  int32  `json:"max_member_count"`
	} `json:"data"`
	Retcode int64 `json:"retcode"`
	//Status string `json:"status"`
	Echo int `json:"echo"`
}

type LastWelcomeInfo struct {
	UserId  int64
	GroupId int64
	Time    int64
}

func (msgQQ *MessageQQ) toStdMessage() *Message {
	msg := new(Message)
	msg.Time = msgQQ.Time
	msg.MessageType = msgQQ.MessageType
	msg.Message = msgQQ.Message
	msg.Message = strings.ReplaceAll(msg.Message, "&#91;", "[")
	msg.Message = strings.ReplaceAll(msg.Message, "&#93;", "]")
	msg.RawId = msgQQ.MessageId
	msg.Platform = "QQ"

	if msgQQ.Data != nil && msgQQ.Data.GroupId != 0 {
		msg.GroupId = FormatDiceIdQQGroup(msgQQ.Data.GroupId)
	}
	if msgQQ.GroupId != 0 {
		msg.GroupId = FormatDiceIdQQGroup(msgQQ.GroupId)
	}
	if msgQQ.Sender != nil {
		msg.Sender.Nickname = msgQQ.Sender.Nickname
		if msgQQ.Sender.Card != "" {
			msg.Sender.Nickname = msgQQ.Sender.Card
		}
		msg.Sender.GroupRole = msgQQ.Sender.Role
		msg.Sender.UserId = FormatDiceIdQQ(msgQQ.Sender.UserId)
	}
	return msg
}

func FormatDiceIdQQ(diceQQ int64) string {
	return fmt.Sprintf("QQ:%s", strconv.FormatInt(diceQQ, 10))
}

func FormatDiceIdQQGroup(diceQQ int64) string {
	return fmt.Sprintf("QQ-Group:%s", strconv.FormatInt(diceQQ, 10))
}

func FormatDiceIdQQCh(userId string) string {
	return fmt.Sprintf("QQ-CH:%s", userId)
}

func FormatDiceIdQQChGroup(GuildId, ChannelId string) string {
	return fmt.Sprintf("QQ-CH-Group:%s-%s", GuildId, ChannelId)
}

func (pa *PlatformAdapterQQOnebot) Serve() int {
	ep := pa.EndPoint
	s := pa.Session
	log := s.Parent.Logger
	dm := s.Parent.Parent
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	pa.InPackGoCqHttpDisconnectedCH = make(chan int, 1)
	session := s

	socket := gowebsocket.New(pa.ConnectUrl)
	pa.Socket = &socket

	socket.OnConnected = func(socket gowebsocket.Socket) {
		ep.State = 1
		log.Info("onebot 连接成功")
		//  {"data":{"nickname":"闃斧鐗岃�佽檸鏈�","user_id":1001},"retcode":0,"status":"ok"}
		pa.GetLoginInfo()
	}

	socket.OnConnectError = func(err error, socket gowebsocket.Socket) {
		log.Info("Recieved connect error: ", err)
		fmt.Println("连接失败")
		pa.InPackGoCqHttpDisconnectedCH <- 2
	}

	// {"channel_id":"3574366","guild_id":"51541481646552899","message":"说句话试试","message_id":"BAC3HLRYvXdDAAAAAAA2il4AAAAAAAAABA==","message_type":"guild","post_type":"mes
	//sage","self_id":2589922907,"self_tiny_id":"144115218748146488","sender":{"nickname":"木落","tiny_id":"222","user_id":222},"sub_type":"channel",
	//"time":1647386874,"user_id":"144115218731218202"}

	// 疑似消息发送成功？等等 是不是可以用来取一下log
	// {"data":{"message_id":-1541043078},"retcode":0,"status":"ok"}
	var lastWelcome *LastWelcomeInfo

	// 注意这几个不能轻易delete，最好整个替换
	tempInviteMap := map[string]int64{}
	tempInviteMap2 := map[string]string{}
	tempGroupEnterSpeechSent := map[string]int64{} // 记录入群致辞的发送时间 避免短时间重复
	tempFriendInviteSent := map[string]int64{}     // gocq会重新发送已经发过的邀请

	socket.OnTextMessage = func(message string, socket gowebsocket.Socket) {
		//if strings.Contains(message, `.`) {
		//	log.Info("...", message)
		//}
		if strings.Contains(message, `"guild_id"`) {
			//log.Info("!!!", message, s.Parent.WorkInQQChannel)
			// 暂时忽略频道消息
			if s.Parent.WorkInQQChannel {
				pa.QQChannelTrySolve(message)
			}
			return
		}

		msgQQ := new(MessageQQ)
		err := json.Unmarshal([]byte(message), msgQQ)

		if err == nil {
			// 心跳包，忽略
			if msgQQ.MetaEventType == "heartbeat" {
				return
			}
			if msgQQ.MetaEventType == "heartbeat" {
				return
			}

			if !ep.Enable {
				pa.InPackGoCqHttpDisconnectedCH <- 3
			}

			msg := msgQQ.toStdMessage()
			ctx := &MsgContext{MessageType: msg.MessageType, EndPoint: ep, Session: session, Dice: session.Parent}

			if msg.Sender.UserId != "" {
				// 用户名缓存
				dm.UserNameCache.Set(msg.Sender.UserId, &GroupNameCacheItem{Name: msg.Sender.Nickname, time: time.Now().Unix()})
			}

			// 获得用户信息
			if msgQQ.Echo == -1 {
				ep.Nickname = msgQQ.Data.Nickname
				ep.UserId = FormatDiceIdQQ(msgQQ.Data.UserId)

				log.Debug("骰子信息已刷新")
				ep.RefreshGroupNum()
				return
			}

			// 获得群信息
			if msgQQ.Echo == -2 {
				if msgQQ.Data != nil {
					groupId := FormatDiceIdQQGroup(msgQQ.Data.GroupId)
					dm.GroupNameCache.Set(groupId, &GroupNameCacheItem{
						msgQQ.Data.GroupName,
						time.Now().Unix(),
					}) // 不论如何，先试图取一下群名

					group := session.ServiceAtNew[groupId]
					if group != nil {
						if msgQQ.Data.MaxMemberCount == 0 {
							// 试图删除自己
							diceId := ep.UserId
							if _, exists := group.ActiveDiceIds[diceId]; exists {
								// 删除自己的登记信息
								delete(group.ActiveDiceIds, diceId)

								if len(group.ActiveDiceIds) == 0 {
									// 如果该群所有账号都被删除了，那么也删掉整条记录
									// 这似乎是个危险操作
									// TODO: 该群下的用户信息实际没有被删除
									group.NotInGroup = true
									delete(session.ServiceAtNew, msg.GroupId)
								}
							}
						} else {
							// 更新群名
							group.GroupName = msgQQ.Data.GroupName
						}

						// 处理被强制拉群的情况
						uid := group.InviteUserId
						banInfo := ctx.Dice.BanList.GetById(uid)
						if banInfo != nil {
							if banInfo.Rank == BanRankBanned && ctx.Dice.BanList.BanBehaviorRefuseInvite {
								// 如果是被ban之后拉群，判定为强制拉群
								if group.EnteredTime > 0 && group.EnteredTime > banInfo.BanTime {
									text := fmt.Sprintf("本次入群为遭遇强制邀请，即将主动退群，因为邀请人%s正处于黑名单上。打扰各位还请见谅。感谢使用海豹核心。", group.InviteUserId)
									ReplyGroupRaw(ctx, &Message{GroupId: groupId}, text, "")
									time.Sleep(1 * time.Second)
									pa.QuitGroup(ctx, groupId)
								}
								return
							}
						}

						// 强制拉群情况2 - 群在黑名单
						banInfo = ctx.Dice.BanList.GetById(groupId)
						if banInfo != nil {
							if banInfo.Rank == BanRankBanned {
								// 如果是被ban之后拉群，判定为强制拉群
								if group.EnteredTime > 0 && group.EnteredTime > banInfo.BanTime {
									text := fmt.Sprintf("被群已被拉黑，即将自动退出，解封请联系骰主。打扰各位还请见谅。感谢使用海豹核心:\n当前情况: %s", banInfo.toText(ctx.Dice))
									ReplyGroupRaw(ctx, &Message{GroupId: groupId}, text, "")
									time.Sleep(1 * time.Second)
									pa.QuitGroup(ctx, groupId)
								}
								return
							}
						}

					} else {
						// TODO: 这玩意的创建是个专业活，等下来弄
						//session.ServiceAtNew[groupId] = GroupInfo{}
					}
					// 这句话太吵了
					//log.Debug("群信息刷新: ", msgQQ.Data.GroupName)
				}
				return
			}

			// 处理加群请求
			if msgQQ.PostType == "request" && msgQQ.RequestType == "group" && msgQQ.SubType == "invite" {
				// {"comment":"","flag":"111","group_id":222,"post_type":"request","request_type":"group","self_id":333,"sub_type":"invite","time":1646782195,"user_id":444}
				ep.RefreshGroupNum()
				pa.GetGroupInfoAsync(msg.GroupId)
				time.Sleep(time.Duration((1.8 + rand.Float64()) * float64(time.Second))) // 稍作等待，也许能拿到群名

				uid := FormatDiceIdQQ(msgQQ.UserId)
				groupName := dm.TryGetGroupName(msg.GroupId)
				userName := dm.TryGetUserName(uid)
				txt := fmt.Sprintf("收到QQ加群邀请: 群组<%s>(%d) 邀请人:<%s>(%d)", groupName, msgQQ.GroupId, userName, msgQQ.UserId)
				log.Info(txt)
				ctx.Notice(txt)
				tempInviteMap[msg.GroupId] = time.Now().Unix()
				tempInviteMap2[msg.GroupId] = uid

				// 邀请人在黑名单上
				banInfo := ctx.Dice.BanList.GetById(uid)
				if banInfo != nil {
					if banInfo.Rank == BanRankBanned && ctx.Dice.BanList.BanBehaviorRefuseInvite {
						pa.SetGroupAddRequest(msgQQ.Flag, msgQQ.SubType, false, "黑名单")
						return
					}
				}

				// 信任模式，如果不是信任，又不是master则拒绝拉群邀请
				isMaster := ctx.Dice.IsMaster(uid)
				if ctx.Dice.TrustOnlyMode && (banInfo.Rank != BanRankTrusted && !isMaster) {
					pa.SetGroupAddRequest(msgQQ.Flag, msgQQ.SubType, false, "只允许骰主设置信任的人拉群")
					return
				}

				// 群在黑名单上
				banInfo = ctx.Dice.BanList.GetById(msg.GroupId)
				if banInfo != nil {
					if banInfo.Rank == BanRankBanned {
						pa.SetGroupAddRequest(msgQQ.Flag, msgQQ.SubType, false, "群黑名单")
						return
					}
				}

				if ctx.Dice.RefuseGroupInvite {
					pa.SetGroupAddRequest(msgQQ.Flag, msgQQ.SubType, false, "设置拒绝加群")
					return
				}

				//time.Sleep(time.Duration((0.8 + rand.Float64()) * float64(time.Second)))
				pa.SetGroupAddRequest(msgQQ.Flag, msgQQ.SubType, true, "")
				return
			}

			// 好友请求
			if msgQQ.PostType == "request" && msgQQ.RequestType == "friend" {
				// 有一个来自gocq的重发问题
				lastTime := tempFriendInviteSent[msgQQ.Flag]
				nowTime := time.Now().Unix()
				if nowTime-lastTime < 20*60 {
					// 保留20s
					return
				}
				tempFriendInviteSent[msgQQ.Flag] = nowTime

				// {"comment":"123","flag":"1647619872000000","post_type":"request","request_type":"friend","self_id":222,"time":1647619871,"user_id":111}
				var comment string
				if msgQQ.Comment != "" {
					comment = strings.TrimSpace(msgQQ.Comment)
					comment = strings.ReplaceAll(comment, "\u00a0", "")
				}

				toMatch := strings.TrimSpace(session.Parent.FriendAddComment)
				willAccept := comment == DiceFormat(ctx, toMatch)
				if toMatch == "" {
					willAccept = true
				}

				if !willAccept {
					// 如果是问题校验，只填写回答即可
					re := regexp.MustCompile(`\n回答:([^\n]+)`)
					m := re.FindAllStringSubmatch(comment, -1)

					items := []string{}
					for _, i := range m {
						items = append(items, i[1])
					}

					re2 := regexp.MustCompile(`\s+`)
					m2 := re2.Split(toMatch, -1)

					if len(m2) == len(items) {
						ok := true
						for i := 0; i < len(m2); i++ {
							if m2[i] != items[i] {
								ok = false
								break
							}
						}
						willAccept = ok
					}
				}

				if comment == "" {
					comment = "(无)"
				} else {
					comment = strconv.Quote(comment)
				}

				txt := fmt.Sprintf("收到QQ好友邀请: 邀请人:%d, 验证信息: %s, 是否自动同意: %t", msgQQ.UserId, comment, willAccept)
				log.Info(txt)
				ctx.Notice(txt)
				time.Sleep(time.Duration((0.8 + rand.Float64()) * float64(time.Second)))

				// 黑名单
				uid := FormatDiceIdQQ(msgQQ.UserId)
				banInfo := ctx.Dice.BanList.GetById(uid)
				if banInfo != nil {
					if banInfo.Rank == BanRankBanned && ctx.Dice.BanList.BanBehaviorRefuseInvite {
						pa.SetFriendAddRequest(msgQQ.Flag, false, "", "黑名单用户")
						return
					}
				}

				if willAccept {
					pa.SetFriendAddRequest(msgQQ.Flag, true, "", "")
				} else {
					pa.SetFriendAddRequest(msgQQ.Flag, false, "", "验证信息不符")
				}
				return
			}

			// 好友通过后
			if msgQQ.NoticeType == "friend_add" && msgQQ.PostType == "notice" {
				// {"notice_type":"friend_add","post_type":"notice","self_id":222,"time":1648239248,"user_id":111}
				go func() {
					// 稍作等待后发送入群致词
					time.Sleep(2 * time.Second)
					uid := FormatDiceIdQQ(msgQQ.UserId)
					welcome := DiceFormatTmpl(ctx, "核心:骰子成为好友")
					log.Infof("与 %s 成为好友，发送好友致辞: %s", uid, welcome)
					pa.SendToPerson(ctx, uid, welcome, "")
				}()
				return
			}

			groupEnterFired := false
			groupEntered := func() {
				if groupEnterFired {
					return
				}
				groupEnterFired = true
				lastTime := tempGroupEnterSpeechSent[msg.GroupId]
				nowTime := time.Now().Unix()

				if nowTime-lastTime < 10 {
					// 10s内只发一次
					return
				}
				tempGroupEnterSpeechSent[msg.GroupId] = nowTime

				// 判断进群的人是自己，自动启动
				gi := SetBotOnAtGroup(ctx, msg.GroupId)
				if tempInviteMap2[msg.GroupId] != "" {
					// 设置邀请人
					gi.InviteUserId = tempInviteMap2[msg.GroupId]
				}
				gi.EnteredTime = nowTime // 设置入群时间
				// 立即获取群信息
				pa.GetGroupInfoAsync(msg.GroupId)
				// fmt.Sprintf("<%s>已经就绪。可通过.help查看指令列表", conn.Nickname)

				time.Sleep(2 * time.Second)
				groupName := dm.TryGetGroupName(msg.GroupId)
				go func() {
					// 稍作等待后发送入群致词
					time.Sleep(1 * time.Second)
					log.Infof("发送入群致辞，群: <%s>(%d)", groupName, msgQQ.GroupId)
					pa.SendToGroup(ctx, msg.GroupId, DiceFormatTmpl(ctx, "核心:骰子进群"), "")
				}()
				txt := fmt.Sprintf("加入QQ群组: <%s>(%d)", groupName, msgQQ.GroupId)
				log.Info(txt)
				ctx.Notice(txt)
			}

			// 入群的另一种情况: 管理员审核
			group := s.ServiceAtNew[msg.GroupId]
			if group == nil && msg.GroupId != "" {
				now := time.Now().Unix()
				if tempInviteMap[msg.GroupId] != 0 && now > tempInviteMap[msg.GroupId] {
					delete(tempInviteMap, msg.GroupId)
					groupEntered()
				}
				//log.Infof("自动激活: 发现无记录群组(%s)，因为已是群成员，所以自动激活", group.GroupId)
			}

			// 入群后自动开启
			if msgQQ.PostType == "notice" && msgQQ.NoticeType == "group_increase" {
				//{"group_id":111,"notice_type":"group_increase","operator_id":0,"post_type":"notice","self_id":333,"sub_type":"approve","time":1646782012,"user_id":333}
				if msgQQ.UserId == msgQQ.SelfId {
					groupEntered()
				} else {
					group := session.ServiceAtNew[msg.GroupId]
					// 进群的是别人，是否迎新？
					// 这里很诡异，当手机QQ客户端审批进群时，入群后会有一句默认发言
					// 此时会收到两次完全一样的某用户入群信息，导致发两次欢迎词
					if group != nil && group.ShowGroupWelcome {
						isDouble := false
						if lastWelcome != nil {
							isDouble = msgQQ.GroupId == lastWelcome.GroupId &&
								msgQQ.UserId == lastWelcome.UserId &&
								msgQQ.Time == lastWelcome.Time
						}
						lastWelcome = &LastWelcomeInfo{
							GroupId: msgQQ.GroupId,
							UserId:  msgQQ.UserId,
							Time:    msgQQ.Time,
						}

						if !isDouble {
							//VarSetValueStr(ctx, "$t新人昵称", "<"+msgQQ.Sender.Nickname+">")
							pa.SendToGroup(ctx, msg.GroupId, DiceFormat(ctx, group.GroupWelcomeMessage), "")
						}
					}
				}
				return
			}

			if msgQQ.PostType == "notice" && msgQQ.NoticeType == "group_decrease" && msgQQ.SubType == "kick_me" {
				// 被踢
				//  {"group_id":111,"notice_type":"group_decrease","operator_id":222,"post_type":"notice","self_id":333,"sub_type":"kick_me","time":1646689414 ,"user_id":333}
				if msgQQ.UserId == msgQQ.SelfId {
					opUid := FormatDiceIdQQ(msgQQ.OperatorId)
					groupName := dm.TryGetGroupName(msg.GroupId)
					userName := dm.TryGetUserName(opUid)

					ctx.Dice.BanList.AddScoreByGroupKicked(opUid, msg.GroupId, ctx)
					txt := fmt.Sprintf("被踢出群: 在QQ群组<%s>(%d)中被踢出，操作者:<%s>(%d)", groupName, msgQQ.GroupId, userName, msgQQ.OperatorId)
					log.Info(txt)
					ctx.Notice(txt)
				}
				return
			}

			if msgQQ.PostType == "notice" && msgQQ.NoticeType == "group_decrease" && msgQQ.SubType == "leave" && msgQQ.OperatorId == msgQQ.SelfId {
				// 群解散
				// {"group_id":564808710,"notice_type":"group_decrease","operator_id":2589922907,"post_type":"notice","self_id":2589922907,"sub_type":"leave","time":1651584460,"user_id":2589922907}
				groupName := dm.TryGetGroupName(msg.GroupId)
				txt := fmt.Sprintf("离开群组或群解散: <%s>(%d)", groupName, msgQQ.GroupId)
				log.Info(txt)
				ctx.Notice(txt)
				return
			}

			if msgQQ.PostType == "notice" && msgQQ.NoticeType == "group_ban" && msgQQ.SubType == "ban" {
				// 禁言
				// {"duration":600,"group_id":111,"notice_type":"group_ban","operator_id":222,"post_type":"notice","self_id":333,"sub_type":"ban","time":1646689567,"user_id":333}
				if msgQQ.UserId == msgQQ.SelfId {
					opUid := FormatDiceIdQQ(msgQQ.OperatorId)
					groupName := dm.TryGetGroupName(msg.GroupId)
					userName := dm.TryGetUserName(opUid)

					ctx.Dice.BanList.AddScoreByGroupMuted(opUid, msg.GroupId, ctx)
					txt := fmt.Sprintf("被禁言: 在群组<%s>(%d)中被禁言，时长%d秒，操作者:<%s>(%d)", groupName, msgQQ.GroupId, msgQQ.Duration, userName, msgQQ.OperatorId)
					log.Info(txt)
					ctx.Notice(txt)
				}
				return
			}

			// 消息撤回
			if msgQQ.PostType == "notice" && msgQQ.NoticeType == "group_recall" {
				group := s.ServiceAtNew[msg.GroupId]
				if group != nil {
					if group.LogOn {
						_ = LogMarkDeleteByMsgId(ctx, group, msgQQ.MessageId)
					}
				}
				return
			}

			// 处理命令
			if msgQQ.MessageType == "group" || msgQQ.MessageType == "private" {
				if msg.Sender.UserId == ep.UserId {
					// 以免私聊时自己发的信息被记录
					// 这里的私聊消息可能是自己发送的
					// 要是群发也可以被记录就好了
					// XXXX {"font":0,"message":"\u003c木落\u003e的今日人品为83","message_id":-358748624,"message_type":"private","post_type":"message_sent","raw_message":"\u003c木落\u003e的今日人
					//品为83","self_id":2589922907,"sender":{"age":0,"nickname":"海豹一号机","sex":"unknown","user_id":2589922907},"sub_type":"friend","target_id":222,"time":1647760835,"use
					//r_id":2589922907}
					return
				}

				//fmt.Println("Recieved message1 " + message)
				session.Execute(ep, msg, false)
			} else {
				fmt.Println("Recieved message " + message)
			}
		} else {
			log.Error("error" + err.Error())
		}
	}

	socket.OnBinaryMessage = func(data []byte, socket gowebsocket.Socket) {
		log.Debug("Recieved binary data ", data)
	}

	socket.OnPingReceived = func(data string, socket gowebsocket.Socket) {
		log.Debug("Recieved ping " + data)
	}

	socket.OnPongReceived = func(data string, socket gowebsocket.Socket) {
		log.Debug("Recieved pong " + data)
	}

	socket.OnDisconnected = func(err error, socket gowebsocket.Socket) {
		log.Info("onebot 服务的连接被对方关闭 ")
		pa.InPackGoCqHttpDisconnectedCH <- 1
	}

	socket.Connect()
	defer func() {
		fmt.Println("socket close")
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("关闭连接时遭遇异常")
					//core.GetLogger().Error(r)
				}
			}()

			// 可能耗时好久
			socket.Close()
		}()
	}()

	for {
		select {
		case <-interrupt:
			log.Info("interrupt")
			pa.InPackGoCqHttpDisconnectedCH <- 0
			return 0
		case val := <-pa.InPackGoCqHttpDisconnectedCH:
			return val
		}
	}
}

func (pa *PlatformAdapterQQOnebot) DoRelogin() bool {
	myDice := pa.Session.Parent
	ep := pa.EndPoint
	if pa.Socket != nil {
		go pa.Socket.Close()
	}
	if pa.UseInPackGoCqhttp {
		if pa.InPackGoCqHttpDisconnectedCH != nil {
			pa.InPackGoCqHttpDisconnectedCH <- -1
		}
		myDice.Logger.Infof("重新启动go-cqhttp进程，对应账号: <%s>(%s)", ep.Nickname, ep.UserId)
		go GoCqHttpServeProcessKill(myDice, ep)
		time.Sleep(5 * time.Second)                 // 上面那个清理有概率卡住，具体不懂，改成等5s
		GoCqHttpServeRemoveSessionToken(myDice, ep) // 删除session.token
		pa.InPackGoCqHttpLastRestrictedTime = 0     // 重置风控时间
		GoCqHttpServe(myDice, ep, pa.InPackGoCqHttpPassword, pa.InPackGoCqHttpProtocol, true)
		return true
	}
	return false
}

func (pa *PlatformAdapterQQOnebot) SetEnable(enable bool) {
	d := pa.Session.Parent
	c := pa.EndPoint
	if enable {
		c.Enable = true
		pa.DiceServing = false

		if pa.UseInPackGoCqhttp {
			GoCqHttpServeProcessKill(d, c)
			time.Sleep(1 * time.Second)
			GoCqHttpServe(d, c, pa.InPackGoCqHttpPassword, pa.InPackGoCqHttpProtocol, true)
			go DiceServe(d, c)
		} else {
			go DiceServe(d, c)
		}
	} else {
		c.Enable = false
		pa.DiceServing = false
		if pa.UseInPackGoCqhttp {
			GoCqHttpServeProcessKill(d, c)
		}
	}
}
