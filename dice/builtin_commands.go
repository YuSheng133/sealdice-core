package dice

import (
	"encoding/json"
	"fmt"
	"github.com/fy0/lockfree"
	"github.com/juliangruber/go-intersect"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
)

/** 这几条指令不能移除 */
func (d *Dice) registerCoreCommands() {
	HelpForBlack := ".ban add user <帐号> (<原因>) //添加个人\n" +
		".ban add group <群号> (<原因>) //添加群组\n" +
		".ban add <统一ID>\n" +
		".ban rm user <帐号> //解黑/移出信任\n" +
		".ban rm group <群号>\n" +
		".ban rm <统一ID> //同上\n" +
		".ban list // 展示列表\n" +
		".ban list ban/warn/trust //只显示被禁用/被警告/信任用户\n" +
		".ban trust <统一ID> //添加信任\n" +
		".ban help //查看帮助\n" +
		"// 统一ID示例: QQ:12345、QQ-Group:12345"
	cmdBlack := &CmdItemInfo{
		Name: "ban",
		Help: HelpForBlack,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			AtSomebodyButNotMe := len(cmdArgs.At) > 0 && !cmdArgs.AmIBeMentioned // 喊的不是当前骰子
			if AtSomebodyButNotMe {
				return CmdExecuteResult{Matched: false, Solved: false}
			}

			cmdArgs.ChopPrefixToArgsWith("add", "rm", "del", "list", "show", "find", "trust")
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if ctx.PrivilegeLevel < 100 {
					ReplyToSender(ctx, msg, fmt.Sprintf("你不具备Master权限"))
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				val, _ := cmdArgs.GetArgN(1)
				getId := func() string {
					if cmdArgs.IsArgEqual(2, "user") {
						val, exists := cmdArgs.GetArgN(3)
						if !exists {
							return ""
						}
						return FormatDiceId(ctx, val, false)
					} else if cmdArgs.IsArgEqual(2, "group") {
						val, exists := cmdArgs.GetArgN(3)
						if !exists {
							return ""
						}
						return FormatDiceId(ctx, val, true)
					}
					ret, _ := cmdArgs.GetArgN(2)
					if !strings.Contains(ret, ":") {
						// 如果不是这种格式，那么放弃
						ret = ""
					}
					return ret
				}

				switch strings.ToLower(val) {
				case "add":
					uid := getId()
					if uid == "" {
						return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
					}
					reason, _ := cmdArgs.GetArgN(4)
					if reason == "" {
						reason = "骰主指令"
					}
					d.BanList.AddScoreBase(uid, d.BanList.ThresholdBan, "骰主指令", reason, ctx)
					ReplyToSender(ctx, msg, fmt.Sprintf("已将用户/群组 %s 加入黑名单，原因: %s", uid, reason))
				case "rm", "del":
					uid := getId()
					if uid == "" {
						return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
					}
					item := d.BanList.GetById(uid)
					if item.Rank == BanRankBanned || item.Rank == BanRankTrusted || item.Rank == BanRankWarn {
						ReplyToSender(ctx, msg, fmt.Sprintf("已将用户/群组 %s 移出%s列表", uid, BanRankText[item.Rank]))
						item.Score = 0
						item.Rank = BanRankNormal
					} else {
						ReplyToSender(ctx, msg, fmt.Sprintf("找不到用户/群组"))
					}
				case "trust":
					uid, _ := cmdArgs.GetArgN(2)
					if !strings.Contains(uid, ":") {
						// 如果不是这种格式，那么放弃
						uid = ""
					}
					if uid == "" {
						return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
					}

					d.BanList.SetTrustById(uid, "骰主指令", "骰主指令")
					ReplyToSender(ctx, msg, fmt.Sprintf("已将用户/群组 %s 加入信任列表", uid))
				case "list", "show":
					text := ""
					_ = d.BanList.Map.Iterate(func(_k interface{}, _v interface{}) error {
						v, ok := _v.(*BanListInfoItem)
						if ok {
							if v.Rank != BanRankNormal {
								text += v.toText(d) + "\n"
							}
						}
						return nil
					})
					if text == "" {
						text = "当前名单:\n<无内容>"
					} else {
						text = "当前名单:\n" + text
					}
					ReplyToSender(ctx, msg, text)
					break
				default:
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["black"] = cmdBlack
	d.CmdMap["ban"] = cmdBlack

	HelpForFind := ".find/查询 <关键字> // 查找文档。关键字可以多个，用空格分割\n" +
		".find <数字ID> // 显示该ID的词条\n" +
		".find --rand // 显示随机词条\n" +
		".find <关键字> --num=10 // 需要更多结果"
	cmdSearch := &CmdItemInfo{
		Name:     "find",
		Help:     HelpForFind,
		LongHelp: "查询指令，通常使用全文搜索(x86版)或快速查询(arm, 移动版)\n" + HelpForFind,
		// 写不下了
		// + "\n注: 默认搭载的《怪物之锤查询》来自蜜瓜包、October整理\n默认搭载的COC《魔法大典》来自魔骨，NULL，Dr.Amber整理\n默认搭载的DND系列文档来自DicePP项目"
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			AtSomebodyButNotMe := len(cmdArgs.At) > 0 && !cmdArgs.AmIBeMentioned // 喊的不是当前骰子
			if AtSomebodyButNotMe {
				return CmdExecuteResult{Matched: false, Solved: false}
			}

			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if d.Parent.IsHelpReloading {
					ReplyToSender(ctx, msg, "帮助文档正在重新装载，请稍后...")
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				var id string
				if cmdArgs.GetKwarg("rand") != nil || cmdArgs.GetKwarg("随机") != nil {
					_id := rand.Uint64()%d.Parent.Help.CurId + 1
					id = strconv.FormatUint(_id, 10)
				}

				if id == "" {
					if _id, exists := cmdArgs.GetArgN(1); exists {
						_, err2 := strconv.ParseInt(_id, 10, 64)
						if err2 == nil {
							id = _id
						}
					}
				}

				if id != "" {
					text, exists := d.Parent.Help.TextMap[id]
					if exists {
						content := d.Parent.Help.GetContent(text, 0)
						ReplyToSender(ctx, msg, fmt.Sprintf("词条: %s:%s\n%s", text.PackageName, text.Title, content))
					} else {
						ReplyToSender(ctx, msg, "未发现对应ID的词条")
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				if _, exists := cmdArgs.GetArgN(1); exists {
					numLimit := 4
					numParam := cmdArgs.GetKwarg("num")
					if numParam != nil {
						_num, err := strconv.ParseInt(numParam.Value, 10, 64)
						if err == nil {
							numLimit = int(_num)
						}
					}

					search, err := d.Parent.Help.Search(ctx, cmdArgs.CleanArgs, false, numLimit)
					if err == nil {
						if len(search.Hits) > 0 {
							var bestResult string
							hasSecond := len(search.Hits) >= 2
							best := d.Parent.Help.TextMap[search.Hits[0].ID]
							others := ""

							for _, i := range search.Hits {
								t := d.Parent.Help.TextMap[i.ID]
								others += fmt.Sprintf("[%s]【%s:%s】 匹配度%.2f\n", i.ID, t.PackageName, t.Title, i.Score)
							}

							var showBest bool
							if hasSecond {
								offset := d.Parent.Help.GetShowBestOffset()
								val := search.Hits[1].Score - search.Hits[0].Score
								if val < 0 {
									val = -val
								}
								if val > float64(offset) {
									showBest = true
								}
								if best.Title == cmdArgs.CleanArgs {
									showBest = true
								}
							} else {
								showBest = true
							}

							if showBest {
								content := d.Parent.Help.GetContent(best, 0)
								bestResult = fmt.Sprintf("最优先结果:\n词条: %s:%s\n%s\n\n", best.PackageName, best.Title, content)
							}

							suffix := d.Parent.Help.GetSuffixText2()
							ReplyToSender(ctx, msg, fmt.Sprintf("%s%s全部结果:\n%s\n使用\".find <序号>\"可查看明细，如.find 123", suffix, bestResult, others))
						} else {
							ReplyToSender(ctx, msg, "未找到搜索结果")
						}
					} else {
						ReplyToSender(ctx, msg, "搜索故障: "+err.Error())
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
					//ReplyToSender(ctx, msg, "想要问什么呢？\n.查询 <数字ID> // 显示该ID的词条\n.查询 <任意文本> // 查询关联内容\n.查询 --rand // 随机词条")
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["查询"] = cmdSearch
	d.CmdMap["find"] = cmdSearch

	HelpForHelp := ".help // 查看本帮助\n" +
		".help 指令 // 查看某指令信息\n" +
		".help 扩展模块 // 查看扩展信息，如.help coc7\n" +
		".help 关键字 // 查看任意帮助，同.find\n" +
		".help reload // 重新加载帮助文档，需要Master权限"
	cmdHelp := &CmdItemInfo{
		Name:     "help",
		Help:     HelpForHelp,
		LongHelp: "帮助指令，用于查看指令帮助和helpdoc中录入的信息\n" + HelpForHelp,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if arg, exists := cmdArgs.GetArgN(1); exists {
					if strings.EqualFold(arg, "reload") {
						if ctx.PrivilegeLevel < 100 {
							ReplyToSender(ctx, msg, fmt.Sprintf("你不具备Master权限"))
						} else {
							dm := d.Parent
							if dm.JustForTest {
								ReplyToSender(ctx, msg, "此指令在展示模式下不可用")
								return CmdExecuteResult{Matched: true, Solved: true}
							}

							if !dm.IsHelpReloading {
								dm.IsHelpReloading = true
								dm.Help.Close()

								dm.InitHelp()
								dm.AddHelpWithDice(dm.Dice[0])
								ReplyToSender(ctx, msg, "帮助文档已经重新装载")
							} else {
								ReplyToSender(ctx, msg, "帮助文档正在重新装载，请稍后...")
							}
						}
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					if cmdArgs.IsArgEqual(1, "骰主", "骰主信息") {
						masterMsg := ctx.Dice.HelpMasterInfo
						ReplyToSender(ctx, msg, masterMsg)
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					if cmdArgs.IsArgEqual(1, "协议", "使用协议") {
						masterMsg := ctx.Dice.HelpMasterLicense
						ReplyToSender(ctx, msg, masterMsg)
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					if d.Parent.IsHelpReloading {
						ReplyToSender(ctx, msg, "帮助文档正在重新装载，请稍后...")
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					search, err := d.Parent.Help.Search(ctx, cmdArgs.CleanArgs, true, 1)
					if err == nil {
						if len(search.Hits) > 0 {
							// 居然会出现 hits[0] 为nil的情况？？
							//a := d.Parent.Help.GetContent(search.Hits[0].ID)
							a := d.Parent.Help.TextMap[search.Hits[0].ID]
							content := d.Parent.Help.GetContent(a, 0)
							ReplyToSender(ctx, msg, fmt.Sprintf("%s:%s\n%s", a.PackageName, a.Title, content))
						} else {
							ReplyToSender(ctx, msg, "未找到搜索结果")
						}
					} else {
						ReplyToSender(ctx, msg, "搜索故障: "+err.Error())
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				text := "海豹核心 " + VERSION + "\n"
				text += "==========================\n"
				text += ".help 骰点/娱乐/跑团/日志" + "\n"
				text += ".help 扩展/其他/关于" + "\n"
				text += ".help 骰主/协议" + "\n"
				text += "官网: sealdice.com" + "\n"
				//text += "手册(荐): https://dice.weizaima.com/manual/" + "\n"
				text += "海豹群: 524364253" + "\n"
				//text += "扩展指令请输入 .ext 和 .ext <扩展名称> 进行查看\n"
				extra := DiceFormatTmpl(ctx, "核心:骰子帮助文本_附加说明")
				if extra != "" {
					text += "--------------------------\n"
					text += extra
				}
				ReplyToSender(ctx, msg, text)
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["help"] = cmdHelp

	cmdBot := &CmdItemInfo{
		Name:     "bot",
		Help:     ".bot on/off/about/bye // 开启、关闭、查看信息、退群",
		LongHelp: "骰子管理:\n.bot on/off/about/bye // 开启、关闭、查看信息、退群",
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			inGroup := msg.MessageType == "group"
			AtSomebodyButNotMe := len(cmdArgs.At) > 0 && !cmdArgs.AmIBeMentioned // 喊的不是当前骰子

			if len(cmdArgs.Args) == 0 || cmdArgs.IsArgEqual(1, "about") {
				if AtSomebodyButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}
				activeCount := 0
				serveCount := 0
				for _, i := range d.ImSession.ServiceAtNew {
					if !i.NotInGroup && i.GroupId != "" {
						if strings.HasPrefix(i.GroupId, "PG-") {
							continue
						}
						if i.Active {
							activeCount += 1
						}
						serveCount += 1
					}
				}
				//lastSavedTimeText := "从未"
				//if d.LastSavedTime != nil {
				//	lastSavedTimeText = d.LastSavedTime.Format("2006-01-02 15:04:05") + " UTC"
				//}
				onlineVer := ""
				if d.Parent.AppVersionOnline != nil {
					ver := d.Parent.AppVersionOnline
					// 如果当前不是最新版，那么提示
					if ver.VersionLatestCode != VERSION_CODE {
						onlineVer = "最新版本: " + ver.VersionLatestDetail + "\n"
					}
				}
				text := fmt.Sprintf("SealDice %s\n%s供职于%d个群，其中%d个处于开启状态", VERSION, onlineVer, serveCount, activeCount)

				if inGroup {
					isActive := ctx.Group != nil && ctx.Group.Active
					activeText := "开启"
					if !isActive {
						activeText = "关闭"
					}
					text += "\n群内工作状态: " + activeText
					ReplyToSender(ctx, msg, text)
				} else {
					ReplyToSender(ctx, msg, text)
				}
			} else {
				if inGroup && !AtSomebodyButNotMe {
					cmdArgs.ChopPrefixToArgsWith("on", "off")
					matchNumber := func() (bool, bool) {
						txt, exists := cmdArgs.GetArgN(2)
						if len(txt) >= 4 {
							if strings.HasSuffix(ctx.EndPoint.UserId, txt) {
								return true, exists
							}
						}
						return false, exists
					}

					if len(cmdArgs.Args) >= 1 {
						if cmdArgs.IsArgEqual(1, "on") {
							isMe, exists := matchNumber()
							if exists && !isMe {
								// 找的不是我
								return CmdExecuteResult{Matched: false, Solved: false}
							}

							SetBotOnAtGroup(ctx, msg.GroupId)
							ctx.Group = ctx.Session.ServiceAtNew[msg.GroupId]
							ctx.IsCurGroupBotOn = true
							// "SealDice 已启用(开发中) " + VERSION
							text := DiceFormatTmpl(ctx, "核心:骰子开启")
							if ctx.Group.LogOn {
								text += "\n请特别注意: 日志记录处于开启状态"
							}
							ReplyToSender(ctx, msg, text)
							return CmdExecuteResult{Matched: true, Solved: true}
						} else if cmdArgs.IsArgEqual(1, "off") {
							isMe, exists := matchNumber()
							if exists && !isMe {
								// 找的不是我
								return CmdExecuteResult{Matched: false, Solved: false}
							}

							//if len(ctx.Group.ActivatedExtList) == 0 {
							//	delete(ctx.Session.ServiceAt, msg.GroupId)
							//} else {
							ctx.Group.Active = false
							//}
							// 停止服务
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:骰子关闭"))
							return CmdExecuteResult{Matched: true, Solved: true}
						} else if cmdArgs.IsArgEqual(1, "bye", "exit", "quit") {
							isMe, exists := matchNumber()
							if exists && !isMe {
								// 找的不是我
								return CmdExecuteResult{Matched: false, Solved: false}
							}

							// 感觉似乎不太必要
							pRequired := 40 // 40邀请者 50管理 60群主 100master
							if ctx.PrivilegeLevel < pRequired {
								ReplyToSender(ctx, msg, fmt.Sprintf("你不是管理员或master"))
								return CmdExecuteResult{Matched: true, Solved: true}
							}
							//code, exists := cmdArgs.GetArgN(2)
							//if exists {
							//	if code == updateCode && updateCode != "0000" {
							//		ReplyToSender(ctx, msg, "3秒后开始重启")
							//		time.Sleep(3 * time.Second)
							//		dm.RebootRequestChan <- 1
							//	} else {
							//		ReplyToSender(ctx, msg, "无效的升级指令码")
							//	}
							//} else {
							//	updateCode = strconv.FormatInt(rand.Int63()%8999+1000, 10)
							//	text := fmt.Sprintf("进程重启:\n如需重启，请输入.master reboot %s 确认进行重启\n重启将花费约2分钟，失败可能导致进程关闭，建议在接触服务器情况下操作。\n当前进程启动时间: %s", updateCode, time.Unix(dm.AppBootTime, 0).Format("2006-01-02 15:04:05"))
							//	ReplyToSender(ctx, msg, text)
							//}

							// 收到指令，5s后将退出当前群组
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:骰子退群预告"))

							userName := ctx.Dice.Parent.TryGetUserName(msg.Sender.UserId)
							_txt := fmt.Sprintf("指令退群: 于群组<%s>(%s)中告别，操作者:<%s>(%s)", ctx.Group.GroupName, msg.GroupId, userName, msg.Sender.UserId)
							d.Logger.Info(_txt)
							ctx.Notice(_txt)
							ctx.Group.Active = false
							time.Sleep(6 * time.Second)
							ctx.Group.NotInGroup = true
							ctx.EndPoint.Adapter.QuitGroup(ctx, msg.GroupId)
							return CmdExecuteResult{Matched: true, Solved: true}
						} else if cmdArgs.IsArgEqual(1, "save") {
							d.Save(false)
							// 数据已保存
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:骰子保存设置"))
							return CmdExecuteResult{Matched: true, Solved: true}
						}
					}
				}
			}

			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["bot"] = cmdBot

	readIdList := func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) []string {
		uidLst := []string{}
		for _, i := range cmdArgs.At {
			if i.UserId == ctx.EndPoint.UserId {
				// 不许添加自己
				continue
			}
			uidLst = append(uidLst, i.UserId)
		}

		if len(cmdArgs.Args) > 1 {
			for _, i := range cmdArgs.Args[1:] {
				if i == "me" {
					uidLst = append(uidLst, ctx.Player.UserId)
					continue
				}
				qq, err := strconv.ParseInt(i, 10, 64)
				if err == nil {
					uid := FormatDiceIdQQ(qq)
					uidLst = append(uidLst, uid)
				}
			}
		}
		return uidLst
	}

	botListHelp := ".botlist add @A @B @C // 标记群内其他机器人，以免发生误触和无限对话\n" +
		".botlist add @A @B --s  // 同上，不过骰子不会做出回复\n" +
		".botlist del @A @B @C // 去除机器人标记\n" +
		".botlist list/show // 查看当前列表"

	cmdBotList := &CmdItemInfo{
		Name:     "botlist",
		Help:     botListHelp,
		LongHelp: "机器人列表:\n" + botListHelp,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsPrivate {
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:提示_私聊不可用"))
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			cmdArgs.ChopPrefixToArgsWith("add", "rm", "del", "show", "list")

			if ctx.IsCurGroupBotOn {
				notMe := cmdArgs.SomeoneBeMentionedButNotMe
				checkSlience := func() bool {
					return notMe || cmdArgs.GetKwarg("s") != nil ||
						cmdArgs.GetKwarg("slience") != nil
				}

				subCmd, _ := cmdArgs.GetArgN(1)
				switch subCmd {
				case "add":
					existsCount := 0
					newCount := 0
					for _, uid := range readIdList(ctx, msg, cmdArgs) {
						if ctx.Group.BotList[uid] {
							existsCount += 1
						} else {
							ctx.Group.BotList[uid] = true
							newCount += 1
						}
					}

					if !checkSlience() {
						ReplyToSender(ctx, msg, fmt.Sprintf("新增标记了%d个帐号，这些账号将被视为机器人。\n因此他们被人@，或主动发出指令时，海豹将不会回复。\n另外对于botlist add/rm，如果群里有多个海豹，只有第一个被@的会回复，其余的执行指令但不回应", newCount))
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				case "del", "rm":
					existsCount := 0
					for _, uid := range readIdList(ctx, msg, cmdArgs) {
						if ctx.Group.BotList[uid] {
							existsCount += 1
							delete(ctx.Group.BotList, uid)
						}
					}

					if !checkSlience() {
						ReplyToSender(ctx, msg, fmt.Sprintf("删除标记了%d个帐号，这些账号将不再被视为机器人。\n海豹将继续回应他们的命令", existsCount))
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				case "list", "show":
					if cmdArgs.SomeoneBeMentionedButNotMeStrict {
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					text := ""
					for i, _ := range ctx.Group.BotList {
						text += "- " + i + "\n"
					}
					if text == "" {
						text = "无"
					}
					ReplyToSender(ctx, msg, fmt.Sprintf("群内其他机器人列表:\n%s", text))
					return CmdExecuteResult{Matched: true, Solved: true}
				default:
					if cmdArgs.SomeoneBeMentionedButNotMeStrict {
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
			} else if ctx.IsPrivate {
				ReplyToSender(ctx, msg, fmt.Sprintf("私聊中不支持这个指令"))
			}

			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["botlist"] = cmdBotList

	reloginFlag := false
	reloginLastTime := int64(0)

	updateCode := "0000"
	masterListHelp := `.master add me // 将自己标记为骰主
.master add @A @B // 将别人标记为骰主
.master del @A @B @C // 去除骰主标记
.master unlock <密码(在UI中查看)> // (当Master被人抢占时)清空骰主列表，并使自己成为骰主
.master list // 查看当前骰主列表
.master reboot // 重新启动(需要二次确认)
.master checkupdate // 检查更新(需要二次确认)
.master relogin // 30s后重新登录，有机会清掉风控(仅master可用)
.master backup // 做一次备份`
	cmdMaster := &CmdItemInfo{
		Name:     "master",
		Help:     masterListHelp,
		LongHelp: "骰主指令:\n" + masterListHelp,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || msg.MessageType == "private" {
				if ctx.IsCurGroupBotOn && cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				cmdArgs.ChopPrefixToArgsWith("unlock", "rm", "del", "add", "checkupdate", "reboot", "backup")
				pRequired := 0
				if len(ctx.Dice.DiceMasters) >= 1 {
					pRequired = 100
				}
				if ctx.PrivilegeLevel < pRequired {
					subCmd, _ := cmdArgs.GetArgN(1)
					if subCmd == "unlock" {
						// 特殊解锁指令
						code, _ := cmdArgs.GetArgN(2)
						if ctx.Dice.UnlockCodeVerify(code) {
							ctx.Dice.MasterClear()
							ctx.Dice.MasterAdd(ctx.Player.UserId)
							ctx.Dice.UnlockCodeUpdate(true) // 强制刷新解锁码
							ReplyToSender(ctx, msg, fmt.Sprintf("你已成为唯一Master"))
						} else {
							ReplyToSender(ctx, msg, fmt.Sprintf("错误的解锁码"))
						}
					}

					return CmdExecuteResult{Matched: true, Solved: true}
				}

				subCmd, _ := cmdArgs.GetArgN(1)
				switch subCmd {
				case "add":
					newCount := 0
					for _, uid := range readIdList(ctx, msg, cmdArgs) {
						if uid != ctx.EndPoint.UserId {
							ctx.Dice.MasterAdd(uid)
							newCount += 1
						}
					}
					ctx.Dice.Save(false)
					ReplyToSender(ctx, msg, fmt.Sprintf("海豹将新增%d位master", newCount))
					return CmdExecuteResult{Matched: true, Solved: true}
				case "unlock":
					ReplyToSender(ctx, msg, fmt.Sprintf("Master，你找我有什么事吗？"))
					return CmdExecuteResult{Matched: true, Solved: true}
				case "del", "rm":
					existsCount := 0
					for _, uid := range readIdList(ctx, msg, cmdArgs) {
						if ctx.Dice.MasterRemove(uid) {
							existsCount += 1
						}
					}
					ctx.Dice.Save(false)
					ReplyToSender(ctx, msg, fmt.Sprintf("海豹移除了%d名master", existsCount))
					return CmdExecuteResult{Matched: true, Solved: true}
				case "relogin":
					if kw := cmdArgs.GetKwarg("cancel"); kw != nil {
						if reloginFlag == true {
							reloginFlag = false
							ReplyToSender(ctx, msg, fmt.Sprintf("已取消重登录"))
						} else {
							ReplyToSender(ctx, msg, fmt.Sprintf("错误: 不存在能够取消的重新登录行为"))
						}
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					doRelogin := func() {
						reloginLastTime = time.Now().Unix()
						ReplyToSender(ctx, msg, fmt.Sprintf("开始执行重新登录"))
						reloginFlag = false
						time.Sleep(1 * time.Second)
						ctx.EndPoint.Adapter.DoRelogin()
					}

					if time.Now().Unix()-reloginLastTime < 5*60 {
						ReplyToSender(ctx, msg, fmt.Sprintf("执行过不久，指令将在%d秒后可以使用", 5*60-(time.Now().Unix()-reloginLastTime)))
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					if kw := cmdArgs.GetKwarg("now"); kw != nil {
						doRelogin()
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					reloginFlag = true
					ReplyToSender(ctx, msg, fmt.Sprintf("将在30s后重新登录，期间可以输入.master relogin --cancel解除\n若遭遇风控，可能会没有任何输出。静等或输入.master relogin --now立即执行\n此指令每5分钟只能执行一次，可能解除风控，也可能使骰子失联。后果自负"))
					go func() {
						time.Sleep(30 * time.Second)
						if reloginFlag {
							doRelogin()
						}
					}()
					return CmdExecuteResult{Matched: true, Solved: true}
				case "backup":
					ReplyToSender(ctx, msg, "开始备份数据")
					err := ctx.Dice.Parent.BackupSimple()
					if err == nil {
						ReplyToSender(ctx, msg, "备份成功！请到UI界面(综合设置-备份)处下载备份，或在骰子backup目录下读取")
					} else {
						d.Logger.Error("骰子备份:", err)
						ReplyToSender(ctx, msg, "备份失败！错误已写入日志。可能是磁盘已满所致，建议立即进行处理！")
					}
				case "checkupdate":
					dm := ctx.Dice.Parent
					if dm.JustForTest {
						ReplyToSender(ctx, msg, "此指令在展示模式下不可用")
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					code, exists := cmdArgs.GetArgN(2)
					if exists {
						if code == updateCode && updateCode != "0000" {
							ReplyToSender(ctx, msg, "开始下载新版本")
							go func() {
								ret := <-dm.UpdateDownloadedChan
								if ret == "" {
									ReplyToSender(ctx, msg, "准备开始升级，服务即将离线")
								} else {
									ReplyToSender(ctx, msg, "升级失败，原因: "+ret)
								}
							}()
							dm.UpdateRequestChan <- 1
						} else {
							ReplyToSender(ctx, msg, "无效的升级指令码")
						}
					} else {
						var text string
						dm.UpdateCheckRequestChan <- 1
						time.Sleep(time.Second) // 等待1s，应该能够取得新版本了。如果获取失败也不至于卡住
						if dm.AppVersionOnline != nil {
							text = fmt.Sprintf("当前本地版本为: %s\n当前线上版本为: %s", VERSION, dm.AppVersionOnline.VersionLatestDetail)
							if dm.AppVersionCode != dm.AppVersionOnline.VersionLatestCode {
								updateCode = strconv.FormatInt(rand.Int63()%8999+1000, 10)
								text += fmt.Sprintf("\n如需升级，请输入.master checkupdate %s 确认进行升级\n升级将花费约2分钟，升级失败可能导致进程关闭，建议在接触服务器情况下操作。\n当前进程启动时间: %s", updateCode, time.Unix(dm.AppBootTime, 0).Format("2006-01-02 15:04:05"))
							}
						} else {
							text = fmt.Sprintf("当前本地版本为: %s\n当前线上版本为: %s", VERSION, "未知")
						}
						ReplyToSender(ctx, msg, text)
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				case "reboot":
					dm := ctx.Dice.Parent
					if dm.JustForTest {
						ReplyToSender(ctx, msg, "此指令在展示模式下不可用")
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					code, exists := cmdArgs.GetArgN(2)
					if exists {
						if code == updateCode && updateCode != "0000" {
							ReplyToSender(ctx, msg, "3秒后开始重启")
							time.Sleep(3 * time.Second)
							dm.RebootRequestChan <- 1
						} else {
							ReplyToSender(ctx, msg, "无效的重启指令码")
						}
					} else {
						updateCode = strconv.FormatInt(rand.Int63()%8999+1000, 10)
						text := fmt.Sprintf("进程重启:\n如需重启，请输入.master reboot %s 确认进行重启\n重启将花费约2分钟，失败可能导致进程关闭，建议在接触服务器情况下操作。\n当前进程启动时间: %s", updateCode, time.Unix(dm.AppBootTime, 0).Format("2006-01-02 15:04:05"))
						ReplyToSender(ctx, msg, text)
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				case "list":
					text := ""
					for _, i := range ctx.Dice.DiceMasters {
						// uid := FormatDiceIdQQ(i)
						text += "- " + i + "\n"
					}
					if text == "" {
						text = "无"
					}
					ReplyToSender(ctx, msg, fmt.Sprintf("Master列表:\n%s", text))
					return CmdExecuteResult{Matched: true, Solved: true}
				default:
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["master"] = cmdMaster

	cmdSend := &CmdItemInfo{
		Name: "send",
		Help: ".send // 向骰主留言",
		LongHelp: "留言指令:\n.send // 向骰主留言\n" +
			".send to <对方ID> 要说的话 // 骰主回复，举例. send to QQ:12345 感谢留言",
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || msg.MessageType == "private" {
				if ctx.IsCurGroupBotOn && cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				val, _ := cmdArgs.GetArgN(1)
				if val == "to" {
					if ctx.PrivilegeLevel >= 100 {
						uid, exists := cmdArgs.GetArgN(2)
						txt := cmdArgs.GetRestArgsFrom(3)
						if exists && strings.HasPrefix(uid, ctx.EndPoint.Platform) && txt != "" {
							isGroup := strings.Contains(uid, "-Group:")
							txt = fmt.Sprintf("本消息由骰主<%s>通过指令发送:\n", ctx.Player.Name) + txt
							if isGroup {
								ReplyGroup(ctx, &Message{GroupId: uid}, txt)
							} else {
								ReplyPerson(ctx, &Message{Sender: SenderBase{UserId: uid}}, txt)
							}
							ReplyToSender(ctx, msg, "信息已经发送至"+uid)
							return CmdExecuteResult{Matched: true, Solved: true}
						} else {
							return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
						}
					} else {
						ReplyToSender(ctx, msg, fmt.Sprintf("你不具备Master权限"))
					}
				} else if val == "help" {
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				} else {
					for _, uid := range ctx.Dice.DiceMasters {
						text := ""

						if ctx.IsCurGroupBotOn {
							text += fmt.Sprintf("一条来自群组<%s>(%s)，作者<%s>(%s)的留言:\n", ctx.Group.GroupName, ctx.Group.GroupId, ctx.Player.Name, ctx.Player.UserId)
						} else {
							text += fmt.Sprintf("一条来自私聊，作者<%s>(%s)的留言:\n", ctx.Player.Name, ctx.Player.UserId)
						}

						text += cmdArgs.CleanArgs
						ctx.EndPoint.Adapter.SendToPerson(ctx, uid, text, "")
						//replyPersonRaw(ctx, val, text, "")
					}
					ReplyToSender(ctx, msg, "您的留言已被记录，另外注意不要滥用此功能，祝您生活愉快，再会。")
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["send"] = cmdSend

	HelpRoll := ".r <表达式> <原因> // 骰点指令\n.rh <表达式> <原因> // 暗骰"
	cmdRoll := &CmdItemInfo{
		Name:     "roll",
		Help:     HelpRoll,
		LongHelp: "骰点:\n" + HelpRoll,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || msg.MessageType == "private" {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				var text string
				var diceResult int64
				var diceResultExists bool
				var detail string
				disableLoadVarname := !(cmdArgs.Command == "rx" || cmdArgs.Command == "rhx")

				if ctx.Dice.CommandCompatibleMode {
					if (cmdArgs.Command == "rd" || cmdArgs.Command == "rhd") && len(cmdArgs.Args) >= 1 {
						if m, _ := regexp.MatchString(`^\d|优势|劣势|\+|-`, cmdArgs.CleanArgs); m {
							cmdArgs.CleanArgs = "d" + cmdArgs.CleanArgs
						}
					}
				}

				var r *VmResult
				var commandInfoItems []interface{}

				rollOne := func() *CmdExecuteResult {
					forWhat := ""
					if len(cmdArgs.Args) >= 1 {
						var err error
						r, detail, err = ctx.Dice.ExprEvalBase(cmdArgs.CleanArgs, ctx, RollExtraFlags{
							DisableLoadVarname: disableLoadVarname,
							DefaultDiceSideNum: getDefaultDicePoints(ctx),
						})

						if r != nil && r.TypeId == 0 {
							diceResult = r.Value.(int64)
							diceResultExists = true
							//return errors.New("错误的类型")
						}

						if err == nil {
							forWhat = r.restInput
						} else {
							errs := string(err.Error())
							if strings.HasPrefix(errs, "E1:") || strings.HasPrefix(errs, "E5:") {
								ReplyToSender(ctx, msg, errs)
								//ReplyGroup(ctx, msg.GroupId, errs)
								return &CmdExecuteResult{Matched: true, Solved: true}
							}
							forWhat = cmdArgs.CleanArgs
						}
					}

					VarSetValueStr(ctx, "$t原因", forWhat)
					if forWhat != "" {
						forWhatText := DiceFormatTmpl(ctx, "核心:骰点_原因")
						VarSetValueStr(ctx, "$t原因句子", forWhatText)
					} else {
						VarSetValueStr(ctx, "$t原因句子", "")
					}

					if diceResultExists {
						detailWrap := ""
						if detail != "" {
							detailWrap = "=" + detail
						}

						// 指令信息标记
						item := map[string]interface{}{
							"expr":   r.Matched,
							"result": diceResult,
							"reason": forWhat,
						}
						if forWhat == "" {
							delete(item, "reason")
						}
						commandInfoItems = append(commandInfoItems, item)

						VarSetValueStr(ctx, "$t表达式文本", r.Matched)
						VarSetValueStr(ctx, "$t计算过程", detailWrap)
						VarSetValueInt64(ctx, "$t计算结果", diceResult)
						//text = fmt.Sprintf("%s<%s>掷出了 %s%s=%d", prefix, ctx.Player.Name, cmdArgs.Args[0], detailWrap, diceResult)
					} else {
						dicePoints := getDefaultDicePoints(ctx)
						val := DiceRoll64(int64(dicePoints))

						// 指令信息标记
						item := map[string]interface{}{
							"expr":       fmt.Sprintf("D%d", dicePoints),
							"reason":     forWhat,
							"dicePoints": dicePoints,
							"result":     val,
						}
						if forWhat == "" {
							delete(item, "reason")
						}
						commandInfoItems = append(commandInfoItems, item)

						VarSetValueStr(ctx, "$t表达式文本", fmt.Sprintf("D%d", dicePoints))
						VarSetValueStr(ctx, "$t计算过程", "")
						VarSetValueInt64(ctx, "$t计算结果", val)
						//text = fmt.Sprintf("%s<%s>掷出了 D%d=%d", prefix, ctx.Player.Name, dicePoints, val)
					}
					return nil
				}

				if cmdArgs.SpecialExecuteTimes > 1 {
					VarSetValueInt64(ctx, "$t次数", int64(cmdArgs.SpecialExecuteTimes))
					if cmdArgs.SpecialExecuteTimes > 12 {
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:骰点_轮数过多警告"))
						return CmdExecuteResult{Matched: true, Solved: false}
					}
					texts := []string{}
					for i := 0; i < cmdArgs.SpecialExecuteTimes; i++ {
						ret := rollOne()
						if ret != nil {
							return *ret
						}
						texts = append(texts, DiceFormatTmpl(ctx, "核心:骰点_单项结果文本"))
					}
					VarSetValueStr(ctx, "$t结果文本", strings.Join(texts, `\n`))
					text = DiceFormatTmpl(ctx, "核心:骰点_多轮")
				} else {
					ret := rollOne()
					if ret != nil {
						return *ret
					}
					VarSetValueStr(ctx, "$t结果文本", DiceFormatTmpl(ctx, "核心:骰点_单项结果文本"))
					text = DiceFormatTmpl(ctx, "核心:骰点")
				}

				isHide := cmdArgs.Command == "rh" || cmdArgs.Command == "rhd"

				// 指令信息
				commandInfo := map[string]interface{}{
					"cmd":    "roll",
					"pcName": ctx.Player.Name,
					"items":  commandInfoItems,
				}
				if isHide {
					commandInfo["hide"] = isHide
				}
				ctx.CommandInfo = commandInfo

				if kw := cmdArgs.GetKwarg("asm"); r != nil && kw != nil {
					if ctx.PrivilegeLevel >= 40 {
						asm := r.Parser.GetAsmText()
						text += "\n" + asm
					}
				}

				if kw := cmdArgs.GetKwarg("ci"); kw != nil {
					info, err := json.Marshal(ctx.CommandInfo)
					if err == nil {
						text += "\n" + string(info)
					} else {
						text += "\n" + "指令信息无法序列化"
					}
				}

				if isHide {
					if msg.Platform == "QQ-CH" {
						ReplyToSender(ctx, msg, "QQ频道内尚不支持暗骰")
						return CmdExecuteResult{Matched: true, Solved: true}
					}

					if ctx.Group != nil {
						if ctx.IsPrivate {
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:提示_私聊不可用"))
						} else {
							ctx.CommandHideFlag = ctx.Group.GroupId
							//prefix := fmt.Sprintf("来自群<%s>(%d)的暗骰，", ctx.Group.GroupName, msg.GroupId)
							prefix := DiceFormatTmpl(ctx, "核心:暗骰_私聊_前缀")
							ReplyGroup(ctx, msg, DiceFormatTmpl(ctx, "核心:暗骰_群内"))
							ReplyPerson(ctx, msg, prefix+text)
						}
					} else {
						ReplyToSender(ctx, msg, text)
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				} else {
					ReplyToSender(ctx, msg, text)
					return CmdExecuteResult{Matched: true, Solved: true}
				}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["r"] = cmdRoll
	d.CmdMap["rd"] = cmdRoll
	d.CmdMap["roll"] = cmdRoll
	d.CmdMap["rh"] = cmdRoll
	d.CmdMap["rhd"] = cmdRoll
	d.CmdMap["rx"] = cmdRoll
	d.CmdMap["rhx"] = cmdRoll

	helpExt := ".ext // 查看扩展列表"
	cmdExt := &CmdItemInfo{
		Name:     "ext",
		Help:     helpExt,
		LongHelp: "群扩展模块管理:\n" + helpExt,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				showList := func() {
					text := "检测到以下扩展(名称-版本-作者)：\n"
					for index, i := range ctx.Dice.ExtList {
						state := "关"
						for _, j := range ctx.Group.ActivatedExtList {
							if i.Name == j.Name {
								state = "开"
								break
							}
						}
						author := i.Author
						if author == "" {
							author = "<未注明>"
						}
						text += fmt.Sprintf("%d. [%s]%s - %s - %s\n", index+1, state, i.Name, i.Version, author)
					}
					text += "使用命令: .ext <扩展名> on/off 可以在当前群开启或关闭某扩展。\n"
					text += "命令: .ext <扩展名> 可以查看扩展介绍及帮助"
					ReplyToSender(ctx, msg, text)
				}

				if len(cmdArgs.Args) == 0 {
					showList()
				}

				if len(cmdArgs.Args) >= 1 {
					var last int
					if len(cmdArgs.Args) >= 2 {
						last = len(cmdArgs.Args)
					}

					if cmdArgs.IsArgEqual(1, "list") {
						showList()
					} else if cmdArgs.IsArgEqual(last, "on") {
						checkConflict := func(ext *ExtInfo) []string {
							actived := []string{}
							for _, i := range ctx.Group.ActivatedExtList {
								actived = append(actived, i.Name)
							}

							if ext.ConflictWith != nil {
								ret := []string{}
								for _, i := range intersect.Simple(actived, ext.ConflictWith) {
									ret = append(ret, i.(string))
								}
								return ret
							}
							return []string{}
						}

						extNames := []string{}
						conflictsAll := []string{}
						for index := 0; index < len(cmdArgs.Args); index++ {
							for _, i := range d.ExtList {
								extName := strings.ToLower(cmdArgs.Args[index])

								if i.Name == extName {
									extNames = append(extNames, extName)
									conflictsAll = append(conflictsAll, checkConflict(i)...)
									ctx.Group.ExtActive(i)
								}
							}
						}

						if len(extNames) == 0 {
							ReplyToSender(ctx, msg, "输入的扩展类别名无效")
						} else {
							text := fmt.Sprintf("打开扩展 %s", strings.Join(extNames, ","))
							if len(conflictsAll) > 0 {
								text += "\n检测到可能冲突的扩展，建议关闭: " + strings.Join(conflictsAll, ",")
								text += "\n对于扩展中存在的同名指令，则越晚开启的扩展，优先级越高。"
							}
							ReplyToSender(ctx, msg, text)
						}
					} else if cmdArgs.IsArgEqual(last, "off") {
						closed := []string{}
						notfound := []string{}
						for index := 0; index < len(cmdArgs.Args); index++ {
							extName := strings.ToLower(cmdArgs.Args[index])
							ei := ctx.Group.ExtInactive(extName)
							if ei != nil {
								closed = append(closed, ei.Name)
							} else {
								notfound = append(notfound, extName)
							}
						}

						var text string

						if len(closed) > 0 {
							text += fmt.Sprintf("关闭扩展: %s", strings.Join(closed, ","))
						} else {
							text += fmt.Sprintf(" 已关闭或未找到: %s", strings.Join(notfound, ","))
						}
						ReplyToSender(ctx, msg, text)
						return CmdExecuteResult{Matched: true, Solved: true}
					} else {
						extName := cmdArgs.Args[0]
						for _, i := range d.ExtList {
							if i.Name == extName {
								text := fmt.Sprintf("> [%s] 版本%s 作者%s\n", i.Name, i.Version, i.Author)
								ReplyToSender(ctx, msg, text+i.GetDescText(i))
								return CmdExecuteResult{Matched: true, Solved: true}
							}
						}
					}
				}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["ext"] = cmdExt

	helpNN := ".nn // 查看当前角色名\n" +
		".nn <角色名> // 改为指定角色名，若有卡片不会连带修改\n" +
		".nn clr // 重置回群名片"
	cmdNN := &CmdItemInfo{
		Name:     "nn",
		Help:     helpNN,
		LongHelp: "角色名设置:\n" + helpNN,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				val, _ := cmdArgs.GetArgN(1)
				switch val {
				case "":
					ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:昵称_当前"))
				case "clr", "reset":
					p := ctx.Player
					p.Name = msg.Sender.Nickname
					VarSetValue(ctx, "$t玩家", &VMValue{VMTypeString, fmt.Sprintf("<%s>", ctx.Player.Name)})
					VarSetValue(ctx, "$t玩家_RAW", &VMValue{VMTypeString, fmt.Sprintf("%s", ctx.Player.Name)})
					ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:昵称_重置"))
				default:
					p := ctx.Player
					VarSetValue(ctx, "$t旧昵称", &VMValue{VMTypeString, fmt.Sprintf("<%s>", ctx.Player.Name)})
					VarSetValue(ctx, "$t旧昵称_RAW", &VMValue{VMTypeString, fmt.Sprintf("%s", ctx.Player.Name)})

					p.Name = cmdArgs.Args[0]
					VarSetValue(ctx, "$t玩家", &VMValue{VMTypeString, fmt.Sprintf("<%s>", ctx.Player.Name)})
					VarSetValue(ctx, "$t玩家_RAW", &VMValue{VMTypeString, fmt.Sprintf("%s", ctx.Player.Name)})

					ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:昵称_改名"))
					//replyGroup(ctx, msg.GroupId, fmt.Sprintf("%s(%d) 的昵称被设定为<%s>", msg.Sender.Nickname, msg.Sender.UserId, p.Name))
				}

				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["nn"] = cmdNN

	d.CmdMap["userid"] = &CmdItemInfo{
		Name: "userid",
		Help: ".userid // 查看当前帐号和群组ID",
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				text := fmt.Sprintf("个人账号ID为 %s", ctx.Player.UserId)
				if !ctx.IsPrivate {
					text += fmt.Sprintf("\n当前群组ID为 %s", ctx.Group.GroupId)
				}

				ReplyToSender(ctx, msg, text)
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}

	helpSet := ".set info// 查看当前面数设置\n" +
		".set dnd/coc // 设置群内骰子面数为20/100，并自动开启对应扩展 \n" +
		".set <面数> // 设置群内骰子面数\n" +
		".set <面数> --my // 设定个人专属骰子面数\n" +
		".set clr // 清除群内骰子面数设置\n" +
		".set clr --my // 清除个人骰子面数设置"
	cmdSet := &CmdItemInfo{
		Name:     "set",
		Help:     helpSet,
		LongHelp: "设定骰子面数:\n" + helpSet,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				p := ctx.Player
				isSetGroup := true
				my := cmdArgs.GetKwarg("my")
				if my != nil {
					isSetGroup = false
				}

				arg1, exists := cmdArgs.GetArgN(1)
				modSwitch := false
				if exists {
					tipText := "\n提示:"
					if strings.EqualFold(arg1, "coc") {
						cmdArgs.Args[0] = "100"
						ctx.Group.ExtActive(d.ExtFind("coc7"))
						//tipText += "如果你执行的是.setcoc(无空格)，可能说明此时coc7扩展并未打开，请运行.ext coc7 on\n"
						tipText += "已切换至100面骰，并自动开启coc7扩展"
						modSwitch = true
					}
					if strings.EqualFold(arg1, "dnd") {
						cmdArgs.Args[0] = "20"
						ctx.Group.ExtActive(d.ExtFind("dnd5e"))
						tipText += "已切换至20面骰，并自动开启dnd5e扩展。若不希望，请执行.ext dnd5e off"
						modSwitch = true
					}
					num, err := strconv.ParseInt(cmdArgs.Args[0], 10, 64)
					if num < 0 {
						num = 0
					}
					if err == nil {
						if isSetGroup {
							ctx.Group.DiceSideNum = num
							if !modSwitch {
								if num == 20 {
									tipText += "20面骰。如果要进行DND游戏，建议执行.set dnd以确保开启dnd5e指令"
								} else if num == 100 {
									tipText += "100面骰。如果要进行COC游戏，建议执行.set coc以确保开启coc7指令"
								}
							}
							if tipText == "\n提示:" {
								tipText = ""
							}
							VarSetValue(ctx, "$t群组骰子面数", &VMValue{VMTypeInt64, ctx.Group.DiceSideNum})
							VarSetValue(ctx, "$t当前骰子面数", &VMValue{VMTypeInt64, getDefaultDicePoints(ctx)})
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:设定默认群组骰子面数")+tipText)
						} else {
							p.DiceSideNum = int(num)
							VarSetValue(ctx, "$t个人骰子面数", &VMValue{VMTypeInt64, int64(ctx.Player.DiceSideNum)})
							VarSetValue(ctx, "$t当前骰子面数", &VMValue{VMTypeInt64, getDefaultDicePoints(ctx)})
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:设定默认骰子面数"))
							//replyGroup(ctx, msg.GroupId, fmt.Sprintf("设定默认骰子面数为 %d", num))
						}
					} else {
						switch arg1 {
						case "clr":
							if isSetGroup {
								ctx.Group.DiceSideNum = 0
							} else {
								p.DiceSideNum = 0
								//replyGroup(ctx, msg.GroupId, fmt.Sprintf("重设默认骰子面数为初始"))
							}
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:设定默认骰子面数_重置"))
						case "help":
							return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
						case "info":
							ReplyToSender(ctx, msg, DiceFormat(ctx, `个人骰子面数: {$t个人骰子面数}\n`+
								`群组骰子面数: {$t群组骰子面数}\n当前骰子面数: {$t当前骰子面数}`))
						default:
							//replyGroup(ctx, msg.GroupId, fmt.Sprintf("设定默认骰子面数: 格式错误"))
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:设定默认骰子面数_错误"))
						}
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["set"] = cmdSet

	helpCh := ".ch save <角色名> // 保存角色，角色名省略则为当前昵称\n" +
		".ch load <角色名> // 加载角色，角色名省略则为当前昵称\n" +
		".ch list // 列出当前角色\n" +
		".ch del/rm <角色名> // 删除角色"
	cmdChar := &CmdItemInfo{
		Name:     "ch",
		Help:     helpCh,
		LongHelp: "角色管理:\n" + helpCh,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				cmdArgs.ChopPrefixToArgsWith("list", "load", "save", "del", "rm")

				getNickname := func() string {
					name, _ := cmdArgs.GetArgN(2)
					if name == "" {
						name = ctx.Player.Name
					}
					return name
				}

				if cmdArgs.IsArgEqual(1, "list") {
					vars := ctx.LoadPlayerGlobalVars()
					characters := []string{}

					_ = vars.ValueMap.Iterate(func(_k interface{}, _v interface{}) error {
						k := _k.(string)
						if strings.HasPrefix(k, "$ch:") {
							characters = append(characters, k[4:])
						}
						return nil
					})
					if len(characters) == 0 {
						ReplyToSender(ctx, msg, fmt.Sprintf("<%s>当前还没有角色列表", ctx.Player.Name))
					} else {
						ReplyToSender(ctx, msg, fmt.Sprintf("<%s>的角色列表为:\n%s", ctx.Player.Name, strings.Join(characters, "\n")))
					}
				} else if cmdArgs.IsArgEqual(1, "load") {
					name := getNickname()
					vars := ctx.LoadPlayerGlobalVars()
					_data, exists := vars.ValueMap.Get("$ch:" + name)
					if exists {
						data := _data.(*VMValue)
						mapData := make(map[string]*VMValue)
						err := JsonValueMapUnmarshal([]byte(data.Value.(string)), &mapData)

						ctx.Player.Vars.ValueMap = lockfree.NewHashMap()
						for k, v := range mapData {
							ctx.Player.Vars.ValueMap.Set(k, v)
						}
						ctx.Player.Vars.LastWriteTime = time.Now().Unix()

						if err == nil {
							ctx.Player.Name = name
							VarSetValue(ctx, "$t玩家", &VMValue{VMTypeString, fmt.Sprintf("<%s>", ctx.Player.Name)})

							//replyToSender(ctx, msg, fmt.Sprintf("角色<%s>加载成功，欢迎回来。", Name))
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_加载成功"))
						} else {
							//replyToSender(ctx, msg, "无法加载角色：序列化失败")
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_序列化失败"))
						}
					} else {
						//replyToSender(ctx, msg, "无法加载角色：你所指定的角色不存在")
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_角色不存在"))
					}
				} else if cmdArgs.IsArgEqual(1, "save") {
					name := getNickname()
					vars := ctx.LoadPlayerGlobalVars()
					v, err := json.Marshal(LockFreeMapToMap(ctx.Player.Vars.ValueMap))

					if err == nil {
						vars.ValueMap.Set("$ch:"+name, &VMValue{
							VMTypeString,
							string(v),
						})
						vars.LastWriteTime = time.Now().Unix()

						VarSetValue(ctx, "$t新角色名", &VMValue{VMTypeString, fmt.Sprintf("<%s>", name)})
						//replyToSender(ctx, msg, fmt.Sprintf("角色<%s>储存成功", Name))
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_储存成功"))
					} else {
						//replyToSender(ctx, msg, "无法储存角色：序列化失败")
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_序列化失败"))
					}
				} else if cmdArgs.IsArgEqual(1, "del", "rm") {
					name := getNickname()
					vars := ctx.LoadPlayerGlobalVars()

					VarSetValue(ctx, "$t新角色名", &VMValue{VMTypeString, fmt.Sprintf("<%s>", name)})
					_, exists := vars.ValueMap.Get("$ch:" + name)
					if exists {
						vars.ValueMap.Del("$ch:" + name)
						vars.LastWriteTime = time.Now().Unix()

						text := DiceFormatTmpl(ctx, "核心:角色管理_删除成功")
						if name == ctx.Player.Name {
							VarSetValue(ctx, "$t新角色名", &VMValue{VMTypeString, fmt.Sprintf("<%s>", msg.Sender.Nickname)})
							text += "\n" + DiceFormatTmpl(ctx, "核心:角色管理_删除成功_当前卡")
							p := ctx.Player
							p.Name = msg.Sender.Nickname
							p.Vars.ValueMap = lockfree.NewHashMap()
							p.Vars.LastWriteTime = time.Now().Unix()
						}

						ReplyToSender(ctx, msg, text)
					} else {
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:角色管理_角色不存在"))
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["角色"] = cmdChar
	d.CmdMap["ch"] = cmdChar
	d.CmdMap["char"] = cmdChar
	d.CmdMap["character"] = cmdChar
	d.CmdMap["pc"] = cmdChar

	botWelcomeHelp := ".welcome on // 开启\n" +
		".welcome off // 关闭\n" +
		".welcome show // 查看当前欢迎语\n" +
		".welcome set <欢迎语> // 设定欢迎语"
	cmdWelcome := &CmdItemInfo{
		Name:     "welcome",
		Help:     botWelcomeHelp,
		LongHelp: "新人入群自动发言设定:\n" + botWelcomeHelp,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsPrivate {
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:提示_私聊不可用"))
				return CmdExecuteResult{Matched: true, Solved: true}
			}

			if ctx.IsCurGroupBotOn {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				pRequired := 50 // 50管理 60群主 100master
				if ctx.PrivilegeLevel < pRequired {
					ReplyToSender(ctx, msg, fmt.Sprintf("你不是管理员或master"))
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				if cmdArgs.IsArgEqual(1, "on") {
					ctx.Group.ShowGroupWelcome = true
					ReplyToSender(ctx, msg, "入群欢迎语已打开")
				} else if cmdArgs.IsArgEqual(1, "off") {
					ctx.Group.ShowGroupWelcome = false
					ReplyToSender(ctx, msg, "入群欢迎语已关闭")
				} else if cmdArgs.IsArgEqual(1, "show") {
					welcome := "<无内容>"
					welcome = ctx.Group.GroupWelcomeMessage
					var info string
					if ctx.Group.ShowGroupWelcome {
						info = "\n状态: 开启"
					} else {
						info = "\n状态: 关闭"
					}
					ReplyToSender(ctx, msg, "当前欢迎语:\n"+welcome+info)
				} else if _, ok := cmdArgs.EatPrefixWith("set"); ok {
					text2 := strings.TrimSpace(cmdArgs.RawArgs[len("set"):])
					ctx.Group.GroupWelcomeMessage = text2
					ctx.Group.ShowGroupWelcome = true
					ReplyToSender(ctx, msg, "当前欢迎语设定为:\n"+text2+"\n入群欢迎语已自动打开(注意，会在bot off时起效)")
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}
	d.CmdMap["welcome"] = cmdWelcome

	cmdReply := &CmdItemInfo{
		Name:     "reply",
		Help:     ".reply on/off",
		LongHelp: "打开或关闭自定义回复:\n.reply on/off",
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			if ctx.IsCurGroupBotOn || ctx.IsPrivate {
				if cmdArgs.SomeoneBeMentionedButNotMe {
					return CmdExecuteResult{Matched: false, Solved: false}
				}

				val, _ := cmdArgs.GetArgN(1)
				switch val {
				case "on":
					onText := "开"
					if ctx.Group.ExtGetActive("reply") == nil {
						onText = "关"
					}
					extReply := ctx.Dice.ExtFind("reply")
					ctx.Group.ExtActive(extReply)
					ReplyToSender(ctx, msg, fmt.Sprintf("已在当前群开启自定义回复(%s➯开)。\n此指令等价于.ext reply on", onText))
				case "off":
					onText := "开"
					if ctx.Group.ExtGetActive("reply") == nil {
						onText = "关"
					}
					ctx.Group.ExtInactive("reply")
					ReplyToSender(ctx, msg, fmt.Sprintf("已在当前群关闭自定义回复(%s➯关)。\n此指令等价于.ext reply off", onText))
				default:
					return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
				}
				return CmdExecuteResult{Matched: true, Solved: true}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}
	d.CmdMap["reply"] = cmdReply
}

func getDefaultDicePoints(ctx *MsgContext) int64 {
	diceSides := int64(ctx.Player.DiceSideNum)
	if diceSides == 0 && ctx.Group != nil {
		diceSides = ctx.Group.DiceSideNum
	}
	if diceSides <= 0 {
		diceSides = 100
	}
	return diceSides
}
