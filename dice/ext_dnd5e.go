package dice

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	ds "github.com/sealdice/dicescript"
)

type RIListItem struct {
	name   string
	val    int64
	detail string
	uid    string
}

type RIList []*RIListItem

func (lst RIList) Len() int {
	return len(lst)
}
func (lst RIList) Swap(i, j int) {
	lst[i], lst[j] = lst[j], lst[i]
}
func (lst RIList) Less(i, j int) bool {
	if lst[i].val == lst[j].val {
		return lst[i].name > lst[j].name
	}
	return lst[i].val > lst[j].val
}

var dndAttrParent = map[string]string{
	"运动": "力量",

	"体操": "敏捷",
	"巧手": "敏捷",
	"隐匿": "敏捷",

	"调查": "智力",
	"奥秘": "智力",
	"历史": "智力",
	"自然": "智力",
	"宗教": "智力",

	"察觉": "感知",
	"洞悉": "感知",
	"驯兽": "感知",
	"医药": "感知",
	"求生": "感知",

	"游说": "魅力",
	"欺瞒": "魅力",
	"威吓": "魅力",
	"表演": "魅力",
}

const NULL_INIT_VAL = math.MaxInt32 // 不使用 MAX_INT64 以保证 JS 环境使用时不会出现潜在问题

func getPlayerNameTempFunc(mctx *MsgContext) string {
	if mctx.Dice.Config.PlayerNameWrapEnable {
		return fmt.Sprintf("<%s>", mctx.Player.Name)
	}
	return mctx.Player.Name
}

func isAbilityScores(name string) bool {
	for _, i := range []string{"力量", "敏捷", "体质", "智力", "感知", "魅力"} {
		if i == name {
			return true
		}
	}
	return false
}

func stpFormat(attrName string) string {
	return "$stp_" + attrName
}

func RegisterBuiltinExtDnd5e(self *Dice) {
	deathSavingStable := func(ctx *MsgContext) {
		VarDelValue(ctx, "DSS")
		VarDelValue(ctx, "DSF")
		if ctx.Player.AutoSetNameTemplate != "" {
			_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
		}
	}

	deathSaving := func(ctx *MsgContext, successPlus int64, failurePlus int64) (int64, int64) {
		readAndAssign := func(name string) int64 {
			var val int64
			v, exists := _VarGetValueV1(ctx, name)

			if !exists {
				VarSetValueInt64(ctx, name, int64(0))
			} else {
				val, _ = v.ReadInt64()
			}
			return val
		}

		val1 := readAndAssign("DSS")
		val2 := readAndAssign("DSF")

		if successPlus != 0 {
			val1 += successPlus
			VarSetValueInt64(ctx, "DSS", val1)
		}

		if failurePlus != 0 {
			val2 += failurePlus
			VarSetValueInt64(ctx, "DSF", val2)
		}

		if ctx.Player.AutoSetNameTemplate != "" {
			_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
		}

		return val1, val2
	}

	deathSavingResultCheck := func(ctx *MsgContext, a int64, b int64) string {
		text := ""
		if a >= 3 {
			text = DiceFormatTmpl(ctx, "DND:死亡豁免_结局_伤势稳定")
			deathSavingStable(ctx)
		}
		if b >= 3 {
			text = DiceFormatTmpl(ctx, "DND:死亡豁免_结局_角色死亡")
			deathSavingStable(ctx)
		}
		return text
	}

	helpSt := ".st 模板 // 录卡模板\n"
	helpSt += ".st show // 展示个人属性\n"
	helpSt += ".st show <属性1> <属性2> ... // 展示特定的属性数值\n"
	helpSt += ".st show <数字> // 展示高于<数字>的属性，如.st show 30\n"
	helpSt += ".st clr/clear // 清除属性\n"
	helpSt += ".st del <属性1> <属性2> ... // 删除属性，可多项，以空格间隔\n"
	helpSt += ".st export // 导出，包括属性和法术位\n"
	helpSt += ".st help // 帮助\n"
	helpSt += ".st <属性>:<值> // 设置属性，技能加值会自动计算。例：.st 感知:20 洞悉:3\n"
	helpSt += ".st <属性>=<值> // 设置属性，等号效果完全相同\n"
	helpSt += ".st <属性>±<表达式> // 修改属性，例：.st hp+1d4\n"
	helpSt += ".st <属性>±<表达式> @某人 // 修改他人属性，例：.st hp+1d4\n"
	helpSt += ".st hp-1d6 --over // 不计算临时生命扣血\n"
	helpSt += "特别的，扣除hp时，会先将其buff值扣除到0。以及增加hp时，hp的值不会超过hpmax\n"
	helpSt += "需要使用coc版本st，请执行.set coc"

	toExport := func(ctx *MsgContext, key string, val *ds.VMValue, tmpl *GameSystemTemplate) string {
		if dndAttrParent[key] != "" && val.TypeId == ds.VMTypeComputedValue {
			cd, _ := val.ReadComputed()
			base, _ := cd.Attrs.Load("base")
			factor, _ := cd.Attrs.Load("factor")
			if base != nil {
				if factor != nil {
					if ds.ValueEqual(factor, ds.NewIntVal(1), true) {
						return fmt.Sprintf("%s*:%s", key, base.ToRepr())
					} else {
						return fmt.Sprintf("%s*%s:%s", key, factor.ToRepr(), base.ToRepr())
					}
				} else {
					return fmt.Sprintf("%s:%s", key, base.ToRepr())
				}
			}
		}

		if isAbilityScores(key) {
			// 如果为主要属性，同时读取豁免值
			attrs, _ := ctx.Dice.AttrsManager.LoadByCtx(ctx)
			stpKey := stpFormat(key)
			// 注: 如果这里改成 eval，是不是即使原始值为computed也可以？
			if v, exists := attrs.LoadX(stpKey); exists && (v.TypeId == ds.VMTypeInt || v.TypeId == ds.VMTypeFloat) {
				if ds.ValueEqual(v, ds.NewIntVal(1), true) {
					return fmt.Sprintf("%s*:%s", key, val.ToRepr())
				} else {
					return fmt.Sprintf("%s*%s:%s", key, v.ToRepr(), val.ToRepr())
				}
			}
		}
		return ""
	}

	cmdSt := getCmdStBase(CmdStOverrideInfo{
		Help:         helpSt,
		TemplateName: "dnd5e",
		CommandSolve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) *CmdExecuteResult {
			val := cmdArgs.GetArgN(1)
			switch val {
			case "模板":
				text := "人物卡模板(第二行文本):\n"
				text += ".dst 力量:10 体质:10 敏捷:10 智力:10 感知:10 魅力:10 hp:10 hpmax:10 熟练:2 运动:0 体操:0 巧手:0 隐匿:0 调查:0 奥秘:0 历史:0 自然:0 宗教:0 察觉:0 洞悉:0 驯兽:0 医药:0 求生:0 游说:0 欺瞒:0 威吓:0 表演:0\n"
				text += "注意: 技能只写修正值，调整值会自动计算。\n熟练写为“运动*:0”，半个熟练“运动*0.5:0”，录卡也可写为.dst 力量=10"
				ReplyToSender(ctx, msg, text)
				return &CmdExecuteResult{Matched: true, Solved: true}
			}
			ctx.setDndReadForVM(false)
			return nil
		},
		ToExport: toExport,
		ToShow: func(ctx *MsgContext, k string, v *ds.VMValue, tmpl *GameSystemTemplate) string {
			// 附加文本，用于处理buff值
			// 对于带buff的值，st show值后面带上 [x] 其中x为本值
			suffixText := ""
			ctx.CreateVmIfNotExists()
			orgV, err := ctx.vm.RunExpr("$org_"+k, true)
			if orgV != nil {
				if orgV.TypeId == ds.VMTypeComputedValue {
					return "" // 一般有特殊的处理，直接放弃
				}

				vOut := orgV.ToString() // 这是原始值，未经buff的
				if vOut != v.ToString() {
					suffixText = fmt.Sprintf("[%s]", vOut)
				}
				if err != nil {
					suffixText = fmt.Sprintf("[%s]", err.Error())
				}
			}

			return fmt.Sprintf("%s:%s%s", k, v.ToString(), suffixText)
		},
		ToMod: func(ctx *MsgContext, args *CmdArgs, i *stSetOrModInfoItem, attrs *AttributesItem, tmpl *GameSystemTemplate) bool {
			over := args.GetKwarg("over")
			attrName := tmpl.GetAlias(i.name)
			if attrName == "hp" && over == nil {
				hpBuff := attrs.Load("$buff_hp")
				if hpBuff == nil {
					hpBuff = ds.NewIntVal(0)
				}

				// 如果是生命值，先试图扣虚血
				vHpBuffVal := hpBuff.MustReadInt()
				// 正盾才做反馈
				if vHpBuffVal > 0 {
					val := vHpBuffVal - i.value.MustReadInt()
					if val >= 0 {
						// 有充足的盾，扣掉，当前伤害改为0
						attrs.Store("$buff_hp", ds.NewIntVal(val))
						i.value = ds.NewIntVal(0)
					} else {
						// 没有充足的盾，盾扣到0，剩下的继续造成伤害
						attrs.Delete("$buff_hp")
						i.value = ds.NewIntVal(val)
					}
				}
			}

			// 处理技能
			parent := dndAttrParent[attrName]
			if parent != "" {
				val := attrs.Load(attrName)

				if val == nil {
					// 如果不存在，先创建
					m := ds.ValueMap{}
					m.Store("base", ds.NewIntVal(0))
					m.Store("factor", ds.NewIntVal(0))

					val = ds.NewComputedValRaw(&ds.ComputedData{
						// Expr: fmt.Sprintf("this.base + ((%s)??0)/2 - 5 + (熟练??0) * this.factor", parent)
						Expr:  fmt.Sprintf("pbCalc(this.base, this.factor, %s)", parent),
						Attrs: &m,
					})
					attrs.Store(attrName, val)
				}

				if val.TypeId == ds.VMTypeComputedValue {
					cd, _ := val.ReadComputed()
					base, _ := cd.Attrs.Load("base")
					if base == nil {
						base = ds.NewIntVal(0)
					}
					var vNew *ds.VMValue
					if i.op == "+" {
						vNew = base.OpAdd(ctx.vm, i.value)
					}
					if i.op == "-" {
						vNew = base.OpSub(ctx.vm, i.value)
					}
					if vNew != nil {
						cd.Attrs.Store("base", vNew)
						return true
					}
				}
			}

			return false
		},
		ToModResult: func(ctx *MsgContext, args *CmdArgs, i *stSetOrModInfoItem, attrs *AttributesItem, tmpl *GameSystemTemplate, theOldValue, theNewValue *ds.VMValue) *ds.VMValue {
			attrName := tmpl.GetAlias(i.name)
			if attrName == "hp" {
				// 获取hpmax
				var curHpMax ds.IntType
				hpMax, maxExists := attrs.LoadX("hpmax")
				if maxExists && hpMax.TypeId == ds.VMTypeInt {
					curHpMax, _ = hpMax.ReadInt()
				}

				// 注: 暂时只考虑简单形式的hpmax的buff
				if hpmaxBuff, exits := attrs.LoadX("$buff_hpmax"); exits {
					maxVal, _ := hpmaxBuff.ReadInt()
					curHpMax += maxVal
					maxExists = true // 任意一个存在，就视为上限存在，即使为0
				}

				newHp, _ := theNewValue.ReadInt()

				if newHp <= 0 {
					var oldValue ds.IntType
					if theOldValue != nil {
						oldValue, _ = theOldValue.ReadInt()
					}

					// 情况1: 超过生命上限，寄了
					if maxExists && -newHp >= curHpMax {
						deathSavingStable(ctx)
						VarSetValue(ctx, "$t伤害点数", ds.NewIntVal(-newHp))
						i.appendedText = DiceFormatTmpl(ctx, "DND:受到伤害_超过HP上限_附加语")
						return ds.NewIntVal(0)
					}

					if oldValue == 0 {
						// 情况2: 已经在昏迷了
						VarSetValue(ctx, "$t伤害点数", ds.NewIntVal(-newHp))
						i.appendedText = DiceFormatTmpl(ctx, "DND:受到伤害_昏迷中_附加语")
						a, b := deathSaving(ctx, 0, 1)
						exText := deathSavingResultCheck(ctx, a, b)
						if exText != "" {
							i.appendedText += "\n" + exText
						}
						return ds.NewIntVal(0)
					} else {
						// 情况3: 进入昏迷
						VarSetValue(ctx, "$t伤害点数", ds.NewIntVal(-newHp))
						i.appendedText = DiceFormatTmpl(ctx, "DND:受到伤害_进入昏迷_附加语")
						return ds.NewIntVal(0)
					}
				} else {
					// 生命值变为大于0，移除死亡豁免标记
					deathSavingStable(ctx)
					if newHp > curHpMax {
						// 限制不超过hpmax
						return ds.NewIntVal(curHpMax)
					}
				}
			}

			return theNewValue
		},
		ToSet: func(ctx *MsgContext, i *stSetOrModInfoItem, attrs *AttributesItem, tmpl *GameSystemTemplate) bool {
			attrName := tmpl.GetAlias(i.name)
			parent := dndAttrParent[attrName]
			if parent != "" {
				m := ds.ValueMap{}
				m.Store("base", i.value)

				if i.extra != nil {
					m.Store("factor", i.extra)
				} else {
					m.Delete("factor")
				}
				i.value = ds.NewComputedValRaw(&ds.ComputedData{
					// Expr: fmt.Sprintf("this.base + ((%s)??0)/2 - 5 + (熟练??0) * this.factor", parent)
					Expr:  fmt.Sprintf("pbCalc(this.base, this.factor, %s)", parent),
					Attrs: &m,
				})
			} else if isAbilityScores(attrName) {
				// 如果为主要属性，同时读取豁免值
				if i.extra != nil {
					attrs.Store(stpFormat(attrName), i.extra)
				} else {
					attrs.Delete(stpFormat(attrName))
				}
			}
			return false
		},
	})

	helpRc := "" +
		".rc <属性> // .rc 力量\n" +
		".rc <属性>豁免 // .rc 力量豁免\n" +
		".rc <表达式> // .rc 力量+3\n" +
		".rc 3# <表达式> // 多重检定\n" +
		".rc 优势 <表达式> // .rc 优势 力量+4\n" +
		".rc 劣势 <表达式> [<原因>] // .rc 劣势 力量+4 推一下试试\n" +
		".rc <表达式> @某人 // 对某人做检定"

	cmdRc := &CmdItemInfo{
		// Pinenutn: 从这里添加是否检查有多次检定，很隐蔽，通过简单研究cmdArgs是看不出来的，尚未清楚此处逻辑来源
		EnableExecuteTimesParse: true,
		Name:                    "rc",
		ShortHelp:               helpRc,
		Help:                    "DND5E 检定:\n" + helpRc,
		AllowDelegate:           true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			// 获取代骰
			mctx := GetCtxProxyFirst(ctx, cmdArgs)
			mctx.DelegateText = ctx.DelegateText
			mctx.Player.TempValueAlias = &_dnd5eTmpl.Alias
			// 参数确认
			val := cmdArgs.GetArgN(1)
			switch val {
			case "", "help":
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			default:
				// 获取参数
				restText := cmdArgs.CleanArgs
				// 检查是否符合要求
				re := regexp.MustCompile(`^(优势|劣势|優勢|劣勢)`)
				m := re.FindString(restText)
				if m != "" {
					m = strings.Replace(m, "優勢", "优势", 1)
					m = strings.Replace(m, "劣勢", "劣势", 1)
					restText = strings.TrimSpace(restText[len(m):])
				}
				// 初始化VM
				mctx.CreateVmIfNotExists()
				// 获取角色模板
				tmpl := mctx.Group.GetCharTemplate(mctx.Dice)
				// 初始化多轮检定结果保存数组
				textList := make([]string, 0)
				// 多轮检定判断
				round := 1
				if cmdArgs.SpecialExecuteTimes > 1 {
					round = cmdArgs.SpecialExecuteTimes
				}
				// 从COC复制来的轮数检查，同时特判一次的情况，防止完全骰不出去点
				if cmdArgs.SpecialExecuteTimes > int(ctx.Dice.Config.MaxExecuteTime) && cmdArgs.SpecialExecuteTimes != 1 {
					VarSetValueStr(ctx, "$t次数", strconv.Itoa(cmdArgs.SpecialExecuteTimes))
					ReplyToSender(mctx, msg, DiceFormatTmpl(mctx, "DND:检定_轮数过多警告"))
					return CmdExecuteResult{Matched: true, Solved: true}
				}
				// commandInfo配置
				var commandInfo = map[string]interface{}{
					"cmd":    "rc",
					"rule":   "dnd5e",
					"pcName": mctx.Player.Name,
					// items的赋值转移到下面
					// "items":  []interface{}{},
				}
				var commandItems = make([]interface{}, 0)
				// 循环N轮
				for range round {
					// 执行预订的code
					mctx.Eval(tmpl.PreloadCode, nil)
					// 为rc设定属性豁免
					mctx.setDndReadForVM(true)
					// 准备要处理的函数，为了能够读取到 d20 的出目，先不加上加值
					// 执行了一次
					expr := fmt.Sprintf("d20%s", m)
					r := mctx.Eval(expr, nil)
					// 执行出错就丢出去
					if r.vm.Error != nil {
						ReplyToSender(mctx, msg, "无法解析表达式: "+restText)
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					// d20结果
					d20Result, _ := r.ReadInt()
					// 设置变量
					VarSetValueInt64(ctx, "$t骰子出目", int64(d20Result))
					diceDetail := r.vm.GetDetailText()
					// 新的表达式，加上加值 etc.
					expr = restText
					r2 := mctx.Eval(expr, nil)
					// 执行出错就再丢出去
					if r2.vm.Error != nil {
						ReplyToSender(mctx, msg, "无法解析表达式: "+restText)
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					// 拿到执行的结果
					reason := r2.vm.RestInput
					if reason == "" {
						reason = restText
					}
					modifier, ok := r2.ReadInt()
					if !ok {
						ReplyToSender(mctx, msg, "无法解析表达式: "+restText)
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					// 这里只能手动格式化，为了保证不丢信息
					detail := fmt.Sprintf("%s + %s", diceDetail, r2.vm.GetDetailText())
					// Pinenutn/bugtower100：猜测这里只是格式化的部分，所以如果做多次检定，这个变量保存最后一次就够了
					VarSetValueStr(ctx, "$t技能", reason)
					VarSetValueStr(ctx, "$t检定过程文本", detail)
					VarSetValueInt64(ctx, "$t检定结果", int64(d20Result+modifier))
					// 添加对应结果文本，若只执行一次，则使用DND检定，否则使用单项文本初始化
					if round == 1 {
						textList = append(textList, DiceFormatTmpl(ctx, "DND:检定"))
					} else {
						textList = append(textList, DiceFormatTmpl(ctx, "DND:检定_单项结果文本"))
					}
					// 添加对应commandItems
					commandItems = append(commandItems, map[string]interface{}{
						"expr":   expr,
						"reason": reason,
						"result": d20Result + modifier,
					})
				}
				// 拼接文本
				// 由于循环内保留了最后一次的部分技能文本，所以这里不需要再初始化一次技能
				var text string
				if round > 1 {
					VarSetValueStr(ctx, "$t结果文本", strings.Join(textList, "\n"))
					VarSetValueStr(ctx, "$t次数", strconv.Itoa(cmdArgs.SpecialExecuteTimes))
					text = DiceFormatTmpl(ctx, "DND:检定_多轮")
				} else {
					// 是单轮检定，不需要组装成多轮的描述
					text = textList[0]
				}
				// 赋值commandItems
				commandInfo["items"] = commandItems
				// 设置对应的Command
				mctx.CommandInfo = commandInfo
				if kw := cmdArgs.GetKwarg("ci"); kw != nil {
					info, err := json.Marshal(mctx.CommandInfo)
					if err == nil {
						text += "\n" + string(info)
					} else {
						text += "\n" + "指令信息无法序列化"
					}
				}

				isHide := cmdArgs.Command == "rah" || cmdArgs.Command == "rch"

				if isHide {
					if msg.Platform == "QQ-CH" {
						ReplyToSender(ctx, msg, "QQ频道内尚不支持暗骰")
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					if ctx.Group != nil {
						if ctx.IsPrivate {
							ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:提示_私聊不可用"))
						} else {
							ctx.CommandHideFlag = ctx.Group.GroupID
							prefix := DiceFormatTmpl(ctx, "核心:暗骰_私聊_前缀")
							ReplyGroup(ctx, msg, DiceFormatTmpl(ctx, "核心:暗骰_群内"))
							ReplyPerson(ctx, msg, prefix+text)
						}
					} else {
						ReplyToSender(ctx, msg, text)
					}
					return CmdExecuteResult{Matched: true, Solved: true}
				}
				ReplyToSender(mctx, msg, text)
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	helpBuff := "" +
		".buff // 展示当前buff\n" +
		".buff clr // 清除buff\n" +
		".buff del <属性1> <属性2> ... // 删除属性，可多项，以空格间隔\n" +
		".buff help // 帮助\n" +
		".buff <属性>:<值> // 设置buff属性，例：.buff 力量:4  奥秘*:0，奥秘临时熟练加成\n" +
		".buff <属性>±<表达式> // 修改属性，例：.buff hp+1d4\n" +
		".buff <属性>±<表达式> @某人 // 修改他人buff属性，例：.buff hp+1d4"

	cmdBuff := getCmdStBase(CmdStOverrideInfo{
		Help:         helpBuff,
		HelpPrefix:   "属性临时加值，语法同st一致:\n",
		TemplateName: "dnd5e",
		CommandSolve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) *CmdExecuteResult {
			val := cmdArgs.GetArgN(1)
			var tmpl *GameSystemTemplate
			if tmpl2, _ := ctx.Dice.GameSystemMap.Load("dnd5e"); tmpl2 != nil {
				tmpl = tmpl2
			}
			attrs, _ := ctx.Dice.AttrsManager.LoadByCtx(ctx)

			switch val {
			case "export":
				return &CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}

			case "del", "rm":
				var nums []string
				var failed []string

				for _, varname := range cmdArgs.Args[1:] {
					vname := tmpl.GetAlias(varname)
					realname := "$buff_" + vname

					if _, exists := attrs.LoadX(realname); exists {
						nums = append(nums, vname)
						attrs.Delete(realname)
					} else {
						failed = append(failed, vname)
					}
				}

				VarSetValueStr(ctx, "$t属性列表", strings.Join(nums, " "))
				VarSetValueInt64(ctx, "$t失败数量", int64(len(failed)))
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "COC:属性设置_删除"))
				return &CmdExecuteResult{Matched: true, Solved: true}

			case "clr", "clear":
				var toDelete []string
				attrs.Range(func(key string, value *ds.VMValue) bool {
					if strings.HasPrefix(key, "$buff_") {
						toDelete = append(toDelete, key)
					}
					return true
				})
				for _, i := range toDelete {
					attrs.Delete(i)
				}
				VarSetValueInt64(ctx, "$t数量", int64(len(toDelete)))
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "COC:属性设置_清除"))
				return &CmdExecuteResult{Matched: true, Solved: true}

			case "show", "list":
				pickItems, _ := cmdStGetPickItemAndLimit(tmpl, cmdArgs)

				var items []string
				// 或者有pickItems，或者当前的变量数量大于0
				if len(pickItems) > 0 {
					for key := range pickItems {
						if value, exists := attrs.LoadX("$buff_" + key); exists {
							items = append(items, fmt.Sprintf("%s:%s", key, value.ToString()))
						} else {
							items = append(items, fmt.Sprintf("%s:0", key))
						}
					}
				} else if attrs.Len() > 0 {
					attrs.Range(func(key string, value *ds.VMValue) bool {
						if strings.HasPrefix(key, "$buff_") {
							items = append(items, fmt.Sprintf("%s:%s", strings.TrimPrefix(key, "$buff_"), value.ToString()))
						}
						return true
					})
				}

				// 每四个一行，拼起来
				itemsPerLine := tmpl.AttrConfig.ItemsPerLine
				if itemsPerLine <= 1 {
					itemsPerLine = 4
				}

				tick := 0
				info := ""
				for _, i := range items {
					tick++
					info += i
					if tick%itemsPerLine == 0 {
						info += "\n"
					} else {
						info += "\t"
					}
				}

				// 再拼点附加信息，然后输出
				if info == "" {
					info = DiceFormatTmpl(ctx, "COC:属性设置_列出_未发现记录")
				}

				VarSetValueStr(ctx, "$t属性信息", info)
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "COC:属性设置_列出"))
				return &CmdExecuteResult{Matched: true, Solved: true}
			}

			ctx.setDndReadForVM(false)
			return nil
		},
		ToSet: func(ctx *MsgContext, i *stSetOrModInfoItem, attrs *AttributesItem, tmpl *GameSystemTemplate) bool {
			attrName := tmpl.GetAlias(i.name)
			i.name = "$buff_" + attrName

			parent := dndAttrParent[attrName]
			if parent != "" {
				m := ds.ValueMap{}
				m.Store("base", i.value)

				if i.extra != nil {
					m.Store("factor", i.extra)
				} else {
					m.Delete("factor")
				}
				i.value = ds.NewComputedValRaw(&ds.ComputedData{
					Expr:  "[this.base, this.factor]", // 他的expr无意义
					Attrs: &m,
				})
			} else if isAbilityScores(attrName) {
				// 如果为主要属性，同时读取豁免值
				if i.extra != nil {
					attrs.Store("$buff_"+stpFormat(attrName), i.extra)
				} else {
					attrs.Delete("$buff_" + stpFormat(attrName))
				}
			}

			return false
		},
		ToMod: func(ctx *MsgContext, args *CmdArgs, i *stSetOrModInfoItem, attrs *AttributesItem, tmpl *GameSystemTemplate) bool {
			attrName := tmpl.GetAlias(i.name)
			i.name = "$buff_" + attrName

			// 处理技能
			parent := dndAttrParent[attrName]
			if parent != "" {
				val := attrs.Load(attrName)

				if val == nil {
					// 如果不存在，先创建
					m := ds.ValueMap{}
					m.Store("base", ds.NewIntVal(0))
					m.Store("factor", ds.NewIntVal(0))

					val = ds.NewComputedValRaw(&ds.ComputedData{
						Expr:  "[this.base, this.factor]",
						Attrs: &m,
					})
					attrs.Store(attrName, val)
				}

				if val.TypeId == ds.VMTypeComputedValue {
					cd, _ := val.ReadComputed()
					base, _ := cd.Attrs.Load("base")
					if base == nil {
						base = ds.NewIntVal(0)
					}
					var vNew *ds.VMValue
					if i.op == "+" {
						vNew = base.OpAdd(ctx.vm, i.value)
					}
					if i.op == "-" {
						vNew = base.OpSub(ctx.vm, i.value)
					}
					if vNew != nil {
						cd.Attrs.Store("base", vNew)
						return true
					}
				}
			}

			return false
		},
	})

	spellSlotsRenew := func(mctx *MsgContext, _ *Message) int {
		num := 0
		for i := 1; i < 10; i++ {
			// _, _ := VarGetValueInt64(mctx, fmt.Sprintf("$法术位_%d", i))
			spellLevelMax, exists := VarGetValueInt64(mctx, fmt.Sprintf("$法术位上限_%d", i))
			if exists {
				num++
				VarSetValueInt64(mctx, fmt.Sprintf("$法术位_%d", i), spellLevelMax)
			}
		}
		return num
	}

	spellSlotsChange := func(mctx *MsgContext, msg *Message, spellLevel int64, num int64) *CmdExecuteResult {
		spellLevelCur, _ := VarGetValueInt64(mctx, fmt.Sprintf("$法术位_%d", spellLevel))
		spellLevelMax, _ := VarGetValueInt64(mctx, fmt.Sprintf("$法术位上限_%d", spellLevel))

		newLevel := spellLevelCur + num
		if newLevel < 0 {
			ReplyToSender(mctx, msg, fmt.Sprintf(`%s无法消耗%d个%d环法术位，当前%d个`, getPlayerNameTempFunc(mctx), -num, spellLevel, spellLevelCur))
			return &CmdExecuteResult{Matched: true, Solved: true}
		}
		if newLevel > spellLevelMax {
			newLevel = spellLevelMax
		}
		VarSetValueInt64(mctx, fmt.Sprintf("$法术位_%d", spellLevel), newLevel)
		if num < 0 {
			ReplyToSender(mctx, msg, fmt.Sprintf(`%s的%d环法术位消耗至%d个，上限%d个`, getPlayerNameTempFunc(mctx), spellLevel, newLevel, spellLevelMax))
		} else {
			ReplyToSender(mctx, msg, fmt.Sprintf(`%s的%d环法术位恢复至%d个，上限%d个`, getPlayerNameTempFunc(mctx), spellLevel, newLevel, spellLevelMax))
		}
		if mctx.Player.AutoSetNameTemplate != "" {
			_, _ = SetPlayerGroupCardByTemplate(mctx, mctx.Player.AutoSetNameTemplate)
		}
		return nil
	}

	helpSS := "" +
		".ss // 查看当前法术位状况\n" +
		".ss init 4 3 2 // 设置1 2 3环的法术位上限，以此类推到9环\n" +
		".ss set 2环 4 // 单独设置某一环的法术位上限，可连写多组，逗号分隔\n" +
		".ss clr // 清除法术位设置\n" +
		".ss rest // 恢复所有法术位(不回复hp)\n" +
		".ss 3环 +1 // 增加一个3环法术位（不会超过上限）\n" +
		".ss lv3 +1 // 增加一个3环法术位 - 另一种写法\n" +
		".ss 3环 -1 // 消耗一个3环法术位，也可以用.cast 3"

	cmdSpellSlot := &CmdItemInfo{
		Name:          "ss",
		ShortHelp:     helpSS,
		Help:          "DND5E 法术位(.ss .法术位):\n" + helpSS,
		AllowDelegate: true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			cmdArgs.ChopPrefixToArgsWith("init", "set")

			val := cmdArgs.GetArgN(1)
			mctx := GetCtxProxyFirst(ctx, cmdArgs)

			switch val {
			case "init":
				reSlot := regexp.MustCompile(`\d+`)
				slots := reSlot.FindAllString(cmdArgs.CleanArgs, -1)
				if len(slots) > 0 {
					var texts []string
					for index, levelVal := range slots {
						val, _ := strconv.ParseInt(levelVal, 10, 64)
						VarSetValueInt64(mctx, fmt.Sprintf("$法术位_%d", index+1), val)
						VarSetValueInt64(mctx, fmt.Sprintf("$法术位上限_%d", index+1), val)
						texts = append(texts, fmt.Sprintf("%d环%d个", index+1, val))
					}
					ReplyToSender(mctx, msg, "为"+getPlayerNameTempFunc(mctx)+"设置法术位: "+strings.Join(texts, ", "))
					if ctx.Player.AutoSetNameTemplate != "" {
						_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
				}

			case "clr":
				attrs, _ := mctx.Dice.AttrsManager.LoadByCtx(mctx)
				for i := 1; i < 10; i++ {
					attrs.Delete(fmt.Sprintf("$法术位_%d", i))
					attrs.Delete(fmt.Sprintf("$法术位上限_%d", i))
				}
				ReplyToSender(mctx, msg, fmt.Sprintf(`%s法术位数据已清除`, getPlayerNameTempFunc(mctx)))
				if ctx.Player.AutoSetNameTemplate != "" {
					_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
				}

			case "rest":
				n := spellSlotsRenew(mctx, msg)
				if n > 0 {
					ReplyToSender(mctx, msg, fmt.Sprintf(`%s的法术位已经完全恢复`, getPlayerNameTempFunc(mctx)))
				} else {
					ReplyToSender(mctx, msg, fmt.Sprintf(`%s并没有设置过法术位`, getPlayerNameTempFunc(mctx)))
				}
				if ctx.Player.AutoSetNameTemplate != "" {
					_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
				}

			case "set":
				reSlot := regexp.MustCompile(`(\d+)[环cC]\s*(\d+)|[lL][vV](\d+)\s+(\d+)`)
				slots := reSlot.FindAllStringSubmatch(cmdArgs.CleanArgs, -1)
				if len(slots) > 0 {
					var texts []string
					for _, oneSlot := range slots {
						level := oneSlot[1]
						if level == "" {
							level = oneSlot[3]
						}
						levelVal := oneSlot[2]
						if levelVal == "" {
							levelVal = oneSlot[4]
						}
						iLevel, _ := strconv.ParseInt(level, 10, 64)
						iLevelVal, _ := strconv.ParseInt(levelVal, 10, 64)

						VarSetValueInt64(mctx, fmt.Sprintf("$法术位_%d", iLevel), iLevelVal)
						VarSetValueInt64(mctx, fmt.Sprintf("$法术位上限_%d", iLevel), iLevelVal)
						texts = append(texts, fmt.Sprintf("%d环%d个", iLevel, iLevelVal))
					}
					ReplyToSender(mctx, msg, "为"+getPlayerNameTempFunc(mctx)+"设置法术位: "+strings.Join(texts, ", "))
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
				}
			case "":
				var texts []string
				for i := 1; i < 10; i++ {
					spellLevelCur, _ := VarGetValueInt64(mctx, fmt.Sprintf("$法术位_%d", i))
					spellLevelMax, exists := VarGetValueInt64(mctx, fmt.Sprintf("$法术位上限_%d", i))
					if exists {
						texts = append(texts, fmt.Sprintf("%d环:%d/%d", i, spellLevelCur, spellLevelMax))
					}
				}
				summary := strings.Join(texts, ", ")
				if summary == "" {
					summary = "没有设置过法术位"
				}
				ReplyToSender(mctx, msg, fmt.Sprintf(`%s的法术位状况: %s`, getPlayerNameTempFunc(mctx), summary))
			case "help":
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			default:
				reSlot := regexp.MustCompile(`(\d+)[环cC]\s*([+-＋－])(\d+)|[lL][vV](\d+)\s*([+-＋－])(\d+)`)
				slots := reSlot.FindAllStringSubmatch(cmdArgs.CleanArgs, -1)
				if len(slots) > 0 {
					for _, oneSlot := range slots {
						level := oneSlot[1]
						if level == "" {
							level = oneSlot[4]
						}
						op := oneSlot[2]
						if op == "" {
							op = oneSlot[5]
						}
						levelVal := oneSlot[3]
						if levelVal == "" {
							levelVal = oneSlot[6]
						}
						iLevel, _ := strconv.ParseInt(level, 10, 64)
						iLevelVal, _ := strconv.ParseInt(levelVal, 10, 64)
						if op == "-" || op == "－" {
							iLevelVal = -iLevelVal
						}

						ret := spellSlotsChange(mctx, msg, iLevel, iLevelVal)
						if ret != nil {
							return *ret
						}
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
				}
			}
			return CmdExecuteResult{Matched: true, Solved: false}
		},
	}

	helpCast := "" +
		".cast 1 // 消耗1个1环法术位\n" +
		".cast 1 2 // 消耗2个1环法术位"

	cmdCast := &CmdItemInfo{
		Name:          "cast",
		ShortHelp:     helpCast,
		Help:          "DND5E 法术位使用(.cast):\n" + helpCast,
		AllowDelegate: true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			val := cmdArgs.GetArgN(1)
			mctx := GetCtxProxyFirst(ctx, cmdArgs)

			switch val { //nolint:gocritic
			default:
				// 该正则匹配: 2 1, 2环1, 2环 1, 2c1, lv2 1
				reSlot := regexp.MustCompile(`(\d+)(?:[环cC]?|\s)\s*(\d+)?|[lL][vV](\d+)(?:\s+(\d+))?`)

				slots := reSlot.FindAllStringSubmatch(cmdArgs.CleanArgs, -1)
				if len(slots) > 0 {
					for _, oneSlot := range slots {
						level := oneSlot[1]
						if level == "" {
							level = oneSlot[3]
						}
						levelVal := oneSlot[2]
						if levelVal == "" {
							levelVal = oneSlot[4]
						}
						if levelVal == "" {
							levelVal = "1"
						}
						iLevel, _ := strconv.ParseInt(level, 10, 64)
						iLevelVal, _ := strconv.ParseInt(levelVal, 10, 64)

						ret := spellSlotsChange(mctx, msg, iLevel, -iLevelVal)
						if ret != nil {
							return *ret
						}
					}
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
				}
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	helpLongRest := "" +
		".长休 // 恢复生命值(必须设置hpmax且hp>0)和法术位 \n" +
		".longrest // 另一种写法"

	cmdLongRest := &CmdItemInfo{
		Name:          "长休",
		ShortHelp:     helpLongRest,
		Help:          "DND5E 长休:\n" + helpLongRest,
		AllowDelegate: true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			val := cmdArgs.GetArgN(1)
			mctx := GetCtxProxyFirst(ctx, cmdArgs)
			tmpl := ctx.Group.GetCharTemplate(ctx.Dice)
			mctx.Player.TempValueAlias = &tmpl.Alias // 防止找不到hpmax

			switch val {
			case "":
				hpText := "没有设置hpmax，无法回复hp"
				hpMax, exists := VarGetValueInt64(mctx, "hpmax")
				if exists {
					hpText = fmt.Sprintf("hp得到了恢复，现为%d", hpMax)
					VarSetValueInt64(mctx, "hp", hpMax)
				}

				n := spellSlotsRenew(mctx, msg)
				ssText := ""
				if n > 0 {
					ssText = "。法术位得到了恢复"
				}
				if ctx.Player.AutoSetNameTemplate != "" {
					_, _ = SetPlayerGroupCardByTemplate(ctx, ctx.Player.AutoSetNameTemplate)
				}
				ReplyToSender(mctx, msg, fmt.Sprintf(`%s的长休: `+hpText+ssText, getPlayerNameTempFunc(mctx)))
			default:
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	helpDeathSavingThrow := "" +
		".死亡豁免 // 进行死亡豁免检定 \n" +
		".ds // 另一种写法\n" +
		".ds +1d4 // 检定时添加1d4的加值\n" +
		".ds 成功±1 // 死亡豁免成功±1，可简写为.ds s±1\n" +
		".ds 失败±1 // 死亡豁免失败±1，可简写为.ds f±1\n" +
		".ds stat // 查看当前死亡豁免情况\n" +
		".ds help // 查看帮助"

	cmdDeathSavingThrow := &CmdItemInfo{
		Name:          "死亡豁免",
		ShortHelp:     helpDeathSavingThrow,
		Help:          "DND5E 死亡豁免:\n" + helpDeathSavingThrow,
		AllowDelegate: true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			mctx := GetCtxProxyFirst(ctx, cmdArgs)
			tmpl := ctx.Group.GetCharTemplate(ctx.Dice)
			mctx.Player.TempValueAlias = &tmpl.Alias

			restText := cmdArgs.CleanArgs
			re := regexp.MustCompile(`^(s|S|成功|f|F|失败)([+-＋－])`)
			m := re.FindStringSubmatch(restText)
			if len(m) > 0 {
				restText = strings.TrimSpace(restText[len(m[0]):])
				isNeg := m[2] == "-" || m[2] == "－"
				r := ctx.Eval(restText, nil)
				if r.vm.Error != nil {
					ReplyToSender(mctx, msg, "错误: 无法解析表达式: "+restText)
					return CmdExecuteResult{Matched: true, Solved: true}
				}
				_v, _ := r.ReadInt()
				v := int64(_v)
				if isNeg {
					v = -v
				}

				var a, b int64
				switch m[1] {
				case "s", "S", "成功":
					a, b = deathSaving(mctx, v, 0)
				case "f", "F", "失败":
					a, b = deathSaving(mctx, 0, v)
				}
				text := fmt.Sprintf("%s当前的死亡豁免情况: 成功%d 失败%d", getPlayerNameTempFunc(mctx), a, b)
				exText := deathSavingResultCheck(mctx, a, b)
				if exText != "" {
					text += "\n" + exText
				}

				ReplyToSender(mctx, msg, text)
				return CmdExecuteResult{Matched: true, Solved: true}
			}

			val := cmdArgs.GetArgN(1)
			switch val {
			case "stat":
				a, b := deathSaving(mctx, 0, 0)
				text := fmt.Sprintf("%s当前的死亡豁免情况: 成功%d 失败%d", getPlayerNameTempFunc(mctx), a, b)
				ReplyToSender(mctx, msg, text)
			case "help":
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			case "":
				fallthrough
			default:
				hp, exists := VarGetValueInt64(mctx, "hp")
				if !exists {
					ReplyToSender(mctx, msg, fmt.Sprintf(`%s未设置生命值，无法进行死亡豁免检定。`, getPlayerNameTempFunc(mctx)))
					return CmdExecuteResult{Matched: true, Solved: true}
				}
				if hp > 0 {
					ReplyToSender(mctx, msg, fmt.Sprintf(`%s生命值大于0(当前为%d)，无法进行死亡豁免检定。`, getPlayerNameTempFunc(mctx), hp))
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				restText := cmdArgs.CleanArgs
				re := regexp.MustCompile(`^(优势|劣势|優勢|劣勢)`)
				m := re.FindString(restText)
				if m != "" {
					restText = strings.TrimSpace(restText[len(m):])
				}
				expr := fmt.Sprintf("d20%s%s", m, restText)
				mctx.CreateVmIfNotExists()
				mctx.setDndReadForVM(true)
				r := mctx.Eval(expr, nil)
				if r.vm.Error != nil {
					ReplyToSender(mctx, msg, "无法解析表达式: "+restText)
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				d20, ok := r.ReadInt()
				if !ok {
					ReplyToSender(mctx, msg, "并非数值类型: "+r.vm.Matched)
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				detail := r.vm.GetDetailText()
				exprToShow := fmt.Sprintf("[%s]", expr)
				if detail != r.ToString() {
					s := r.ToString()
					exprToShow, _ = strings.CutPrefix(detail, s)
				}

				if d20 == 20 {
					deathSavingStable(mctx)
					VarSetValueInt64(mctx, "hp", 1)
					suffix := DiceFormatTmpl(mctx, "DND:死亡豁免_D20_附加语")
					ReplyToSender(mctx, msg, fmt.Sprintf(`%s的死亡豁免检定: %s=%d %s`, getPlayerNameTempFunc(mctx), exprToShow, d20, suffix))
				} else if d20 == 1 {
					suffix := DiceFormatTmpl(mctx, "DND:死亡豁免_D1_附加语")
					text := fmt.Sprintf(`%s的死亡豁免检定: %s=%d %s`, getPlayerNameTempFunc(mctx), exprToShow, d20, suffix)
					a, b := deathSaving(mctx, 0, 2)
					exText := deathSavingResultCheck(mctx, a, b)
					if exText != "" {
						text += "\n" + exText
					}
					text += fmt.Sprintf("\n当前情况: 成功%d 失败%d", a, b)
					ReplyToSender(mctx, msg, text)
				} else if d20 >= 10 {
					suffix := DiceFormatTmpl(mctx, "DND:死亡豁免_成功_附加语")
					text := fmt.Sprintf(`%s的死亡豁免检定: %s=%d %s`, getPlayerNameTempFunc(mctx), exprToShow, d20, suffix)
					a, b := deathSaving(mctx, 1, 0)
					exText := deathSavingResultCheck(mctx, a, b)
					if exText != "" {
						text += "\n" + exText
					}
					text += fmt.Sprintf("\n当前情况: 成功%d 失败%d", a, b)
					ReplyToSender(mctx, msg, text)
				} else {
					suffix := DiceFormatTmpl(mctx, "DND:死亡豁免_失败_附加语")
					text := fmt.Sprintf(`%s的死亡豁免检定: %s=%d %s`, getPlayerNameTempFunc(mctx), exprToShow, d20, suffix)
					a, b := deathSaving(mctx, 0, 1)
					exText := deathSavingResultCheck(mctx, a, b)
					if exText != "" {
						text += "\n" + exText
					}
					text += fmt.Sprintf("\n当前情况: 成功%d 失败%d", a, b)
					ReplyToSender(mctx, msg, text)
				}
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	helpDnd := ".dnd [<数量>] // 制卡指令，返回<数量>组人物属性，最高为10次\n" +
		".dndx [<数量>] // 制卡指令，但带有属性名，最高为10次"

	cmdDnd := &CmdItemInfo{
		Name:      "dnd",
		ShortHelp: helpDnd,
		Help:      "DND5E制卡指令:\n" + helpDnd,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			isMode2 := cmdArgs.Command == "dndx"
			n := cmdArgs.GetArgN(1)
			val, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				if n == "" {
					val = 1 // 数量不存在时，视为1次
				} else {
					return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
				}
			}
			if val > 10 {
				val = 10
			}
			var i int64

			var ss []string
			for i = 0; i < val; i++ {
				if isMode2 {
					r := ctx.EvalFString(`力量:{$t1=4d6k3} 体质:{$t2=4d6k3} 敏捷:{$t3=4d6k3} 智力:{$t4=4d6k3} 感知:{$t5=4d6k3} 魅力:{$t6=4d6k3} 共计:{$tT=$t1+$t2+$t3+$t4+$t5+$t6}`, nil)
					if r.vm.Error != nil {
						break
					}
					result := r.ToString() + "\n"
					ss = append(ss, result)
				} else {
					r := ctx.EvalFString(`{4d6k3}, {4d6k3}, {4d6k3}, {4d6k3}, {4d6k3}, {4d6k3}`, nil)
					if r.vm.Error != nil {
						break
					}
					result := r.ToString()
					var nums Int64SliceDesc
					total := int64(0)
					for _, i := range strings.Split(result, ", ") {
						val, _ := strconv.ParseInt(i, 10, 64)
						nums = append(nums, val)
						total += val
					}
					sort.Sort(nums)

					var items []string
					for _, i := range nums {
						items = append(items, strconv.FormatInt(i, 10))
					}

					ret := fmt.Sprintf("[%s] = %d\n", strings.Join(items, ", "), total)
					ss = append(ss, ret)
				}
			}
			sep := DiceFormatTmpl(ctx, "DND:制卡_分隔符")
			info := strings.Join(ss, sep)
			VarSetValueStr(ctx, "$t制卡结果文本", info)
			var text string
			if isMode2 {
				text = DiceFormatTmpl(ctx, "DND:制卡_预设模式")
			} else {
				text = DiceFormatTmpl(ctx, "DND:制卡_自由分配模式")
			}
			ReplyToSender(ctx, msg, text)
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}
	helpRi := `.ri 小明 // 格式1，值为D20
.ri 12 张三 // 格式2，值12(只能写数字)
.ri +2 李四 // 格式3，值为D20+2
.ri =D10+3 王五 // 格式4，值为D10+3
.ri 张三, +2 李四, =D10+3 王五 // 设置全部
.ri 优势 张三, 劣势-1 李四 // 支持优势劣势`
	cmdRi := &CmdItemInfo{
		Name:          "ri",
		ShortHelp:     helpRi,
		Help:          "先攻设置:\n" + helpRi,
		AllowDelegate: true,
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			text := cmdArgs.CleanArgs
			mctx := GetCtxProxyFirst(ctx, cmdArgs)

			if cmdArgs.IsArgEqual(1, "help") {
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			}

			readOne := func() (int, string, int64, string, string) {
				text = strings.TrimSpace(text)
				var name string
				var val int64
				var detail string
				var exprExists bool
				var uid string

				if strings.HasPrefix(text, "+") {
					// 加值情况1，D20+
					r := ctx.Eval("d20"+text, nil)
					if r.vm.Error != nil {
						// 情况1，加值输入错误
						return 1, name, val, detail, ""
					}
					detail = r.vm.GetDetailText()
					val = int64(r.MustReadInt())
					text = r.vm.RestInput
					exprExists = true
				} else if strings.HasPrefix(text, "-") {
					// 加值情况1.1，D20-
					r := ctx.Eval("d20"+text, nil)
					if r.vm.Error != nil {
						// 情况1，加值输入错误
						return 1, name, val, detail, ""
					}
					detail = r.vm.GetDetailText()
					val = int64(r.MustReadInt())
					text = r.vm.RestInput
					exprExists = true
				} else if strings.HasPrefix(text, "=") {
					// 加值情况1，=表达式
					r := ctx.Eval(text[1:], nil)
					if r.vm.Error != nil {
						// 情况1，加值输入错误
						return 1, name, val, detail, ""
					}
					val = int64(r.MustReadInt())
					detail = r.vm.GetDetailText()
					text = r.vm.RestInput
					exprExists = true
				} else if strings.HasPrefix(text, "优势") || strings.HasPrefix(text, "劣势") {
					// 优势/劣势
					r := ctx.Eval("d20"+text, nil)
					if r.vm.Error != nil {
						// 优势劣势输入错误
						return 2, name, val, detail, ""
					}
					detail = r.vm.GetDetailText()
					val = int64(r.MustReadInt())
					text = r.vm.RestInput
					exprExists = true
				} else {
					// 加值情况3，数字
					reNum := regexp.MustCompile(`^(\d+)`)
					m := reNum.FindStringSubmatch(text)
					if len(m) > 0 {
						val, _ = strconv.ParseInt(m[0], 10, 64)
						text = text[len(m[0]):]
						exprExists = true
					}
				}

				// 清理读取了第一项文本之后的空格
				text = strings.TrimSpace(text)

				if strings.HasPrefix(text, ",") || strings.HasPrefix(text, "，") || text == "" {
					// 句首有,的话，吃掉
					text = strings.TrimPrefix(text, ",")
					text = strings.TrimPrefix(text, "，")
					// 情况1，名字是自己
					name = mctx.Player.Name
					// replace any space or \n with _
					name = strings.ReplaceAll(name, " ", "_")
					name = strings.ReplaceAll(name, "\n", "_")
					// 情况2，名字是自己，没有加值
					if !exprExists {
						val = int64(ds.Roll(nil, 20, 0))
					}
					uid = mctx.Player.UserID
					return 0, name, val, detail, uid
				}

				// 情况3: 是名字
				reName := regexp.MustCompile(`^([^\s\d,，][^\s,，]*)\s*[,，]?`)
				m := reName.FindStringSubmatch(text)
				if len(m) > 0 {
					name = m[1]
					text = text[len(m[0]):]
					if !exprExists {
						val = int64(ds.Roll(nil, 20, 0))
					}
				} else {
					// 不知道是啥，报错
					return 2, name, val, detail, ""
				}

				return 0, name, val, detail, ""
			}

			solved := true
			tryOnce := true
			var items RIList

			for tryOnce || text != "" {
				code, name, val, detail, uid := readOne()
				items = append(items, &RIListItem{name, val, detail, uid})

				if code != 0 {
					solved = false
					break
				}
				tryOnce = false
			}

			if solved {
				riList := (RIList{}).LoadByCurGroup(ctx)

				textOut := DiceFormatTmpl(mctx, "DND:先攻_设置_前缀")
				sort.Sort(items)
				if riList.Len() == 0 {
					VarSetValueInt64(ctx, "$g当前回合先攻值", NULL_INIT_VAL)
				}
				for order, i := range items {
					var detail string
					if i.detail != "" {
						detail = i.detail + "="
					}
					textOut += fmt.Sprintf("%2d. %s: %s%d\n", order+1, i.name, detail, i.val)

					item := riList.GetExists(i.name)
					if item == nil {
						curInitVal, _ := VarGetValueInt64(ctx, "$g当前回合先攻值")
						if i.val > curInitVal {
							round, _ := VarGetValueInt64(ctx, "$g回合数")
							// 当前先攻值不变，修改回合数
							VarSetValueInt64(ctx, "$g回合数", round+1)
						}
						riList = append(riList, i)
					} else {
						item.val = i.val
					}
				}

				sort.Sort(riList)
				riList.SaveToGroup(ctx)
				ReplyToSender(ctx, msg, textOut)
			} else {
				ReplyToSender(ctx, msg, DiceFormatTmpl(mctx, "DND:先攻_设置_格式错误"))
			}
			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	/**
	Note(Szzrain) at 2025-3-23:
	这里解释一下战斗轮机制运行的原理。
	1，首先，在先攻列表为空时，添加任何单位会将 {$g当前回合先攻值} 设定为 INT_MAX 但如果先攻列表不为空，那么添加任何单位都不会更改 {$g当前回合先攻值}
	2. 只有在进行 .init ed 时才会将 {$g当前回合先攻值} 设定为新单位的先攻值
	3. 在进行 .init del 时，如果删除的单位是当前回合的单位，那么 {$g当前回合先攻值} 会设定为下一个单位的先攻值，如果删除的单位不是当前回合的单位，那么 {$g当前回合先攻值} 不会改变
	4. 在新一轮时，会将 {$g当前回合先攻值} 设定为下一个单位的先攻值，而不是 INT_MAX
	5. 在进行 .init clr 时，会将 {$g当前回合先攻值} 设定为 INT_MAX 如果 .init del 删除了最后一个单位，那么 {$g当前回合先攻值} 会设定为 INT_MAX
	6. {$g回合数} 会记录当前是第几回合，用于先攻值相同时的辅助处理。但是在添加/删除单位时 {$g当前回合先攻值} 的判断优先度更高
	*/
	cmdInit := &CmdItemInfo{
		Name: "init",
		ShortHelp: ".init // 查看先攻列表\n" +
			".init del <单位1> <单位2> ... // 从先攻列表中删除\n" +
			".init set <单位名称> <先攻表达式> // 设置单位的先攻\n" +
			".init clr // 清除先攻列表\n" +
			".init end // 结束一回合" +
			".init help // 显示本帮助",
		Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
			cmdArgs.ChopPrefixToArgsWith("del", "set", "rm", "ed")
			n := cmdArgs.GetArgN(1)
			switch n {
			case "", "list":
				textOut := DiceFormatTmpl(ctx, "DND:先攻_查看_前缀")
				riList := (RIList{}).LoadByCurGroup(ctx)

				round, _ := VarGetValueInt64(ctx, "$g回合数")

				for order, i := range riList {
					textOut += fmt.Sprintf("%2d. %s: %d\n", order+1, i.name, i.val)
				}

				if len(riList) == 0 {
					textOut += "- 没有找到任何单位"
				} else {
					if len(riList) <= int(round) || round < 0 {
						round = 0
					}
					rounder := riList[round]
					textOut += fmt.Sprintf("当前回合：%s", rounder.name)
				}

				ReplyToSender(ctx, msg, textOut)
			case "ed", "end":
				lst := (RIList{}).LoadByCurGroup(ctx)
				round, _ := VarGetValueInt64(ctx, "$g回合数")
				if len(lst) == 0 {
					ReplyToSender(ctx, msg, "先攻列表为空")
					break
				}
				round = (round + 1) % int64(len(lst))

				setInitNextRoundVars(ctx, lst, round)
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "DND:先攻_下一回合"))
			case "del", "rm":
				tryDeleteMembersInInitList := func(deleteNames []string, riList RIList) (newList RIList, textOut strings.Builder, ok bool) {
					if len(riList) == 0 {
						textOut.WriteString("- 没有找到任何单位[先攻列表为空]\n")
						return riList, textOut, false
					}
					round, _ := VarGetValueInt64(ctx, "$g回合数")
					round %= int64(len(riList))
					toDeleted := map[string]bool{}
					for _, i := range deleteNames {
						toDeleted[i] = true
					}

					delCounter := 0

					preCurrent := 0 // 每有一个在当前单位前面的单位被删除, 当前单位下标需要减 1
					for index, i := range riList {
						if !toDeleted[i.name] {
							newList = append(newList, i)
						} else {
							delCounter++
							textOut.WriteString(fmt.Sprintf("%2d. %s\n", delCounter, i.name))

							if int64(index) < round {
								preCurrent++
							}
						}
					}
					current := *riList[round]
					currentDeleted := toDeleted[current.name]

					round -= int64(preCurrent)
					if round >= int64(len(newList)) {
						round = 0
					}
					VarSetValueInt64(ctx, "$g回合数", round)

					if delCounter == 0 {
						textOut.WriteString("- 没有找到任何单位\n")
						return newList, textOut, false
					}

					newList.SaveToGroup(ctx)
					if currentDeleted {
						if len(newList) == 0 {
							VarSetValueInt64(ctx, "$g当前回合先攻值", NULL_INIT_VAL)
							textOut.WriteString(DiceFormatTmpl(ctx, "DND:先攻_清除列表"))
						} else {
							setInitNextRoundVars(ctx, newList, round)
							// Note(Xiangze Li): 这是为了让回合结束的角色显示为被删除的角色，而不是当前角色的上一个
							VarSetValueStr(ctx, "$t当前回合角色名", current.name)
							VarSetValueStr(ctx, "$t当前回合at", AtBuild(current.uid))
							textOut.WriteString(DiceFormatTmpl(ctx, "DND:先攻_下一回合"))
						}
					}
					return newList, textOut, true
				}

				nameWithSpace, _ := cmdArgs.EatPrefixWith("del", "rm")
				riList := (RIList{}).LoadByCurGroup(ctx)
				_, textOut, ok := tryDeleteMembersInInitList([]string{nameWithSpace}, riList)
				if !ok {
					_, textOut, _ = tryDeleteMembersInInitList(cmdArgs.Args[1:], riList)
				}
				textToSend := DiceFormatTmpl(ctx, "DND:先攻_移除_前缀") + textOut.String()

				ReplyToSender(ctx, msg, textToSend)
			case "set":
				name := cmdArgs.GetArgN(2)
				exists := name != ""
				arg3 := cmdArgs.GetArgN(3)
				exists2 := arg3 != ""
				if !exists || !exists2 {
					ReplyToSender(ctx, msg, "错误的格式，应为: .init set <单位名称> <先攻表达式>")
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				expr := strings.Join(cmdArgs.Args[2:], "")
				r := ctx.Eval(expr, nil)
				if r.vm.Error != nil || r.TypeId != ds.VMTypeInt {
					ReplyToSender(ctx, msg, "错误的格式，应为: .init set <单位名称> <先攻表达式>")
					return CmdExecuteResult{Matched: true, Solved: true}
				}

				riList := (RIList{}).LoadByCurGroup(ctx)
				added := false
				for _, i := range riList {
					if i.name == name {
						i.val = int64(r.MustReadInt())
						added = true
						break
					}
				}
				if !added {
					if len(riList) == 0 {
						VarSetValueInt64(ctx, "$g当前回合先攻值", NULL_INIT_VAL)
					} else {
						curInitVal, _ := VarGetValueInt64(ctx, "$g当前回合先攻值")
						if int64(r.MustReadInt()) > curInitVal {
							round, _ := VarGetValueInt64(ctx, "$g回合数")
							// 当前先攻值不变，修改回合数
							VarSetValueInt64(ctx, "$g回合数", round+1)
						}
					}
					riList = append(riList, &RIListItem{name, int64(r.MustReadInt()), "", ""})
				}
				sort.Sort(riList)

				VarSetValueStr(ctx, "$t表达式", expr)
				VarSetValueStr(ctx, "$t目标", name)
				VarSetValueStr(ctx, "$t计算过程", r.vm.GetDetailText())
				VarSetValue(ctx, "$t点数", &r.VMValue)
				textOut := DiceFormatTmpl(ctx, "DND:先攻_设置_指定单位")

				riList.SaveToGroup(ctx)
				ReplyToSender(ctx, msg, textOut)
			case "clr", "clear":
				(RIList{}).SaveToGroup(ctx)
				VarSetValueInt64(ctx, "$g当前回合先攻值", NULL_INIT_VAL)
				ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "DND:先攻_清除列表"))
				VarSetValueInt64(ctx, "$g回合数", 0)
			case "help":
				return CmdExecuteResult{Matched: true, Solved: true, ShowHelp: true}
			}

			return CmdExecuteResult{Matched: true, Solved: true}
		},
	}

	theExt := &ExtInfo{
		Name:       "dnd5e", // 扩展的名称，需要用于开启和关闭指令中，写简短点
		Version:    "1.0.0",
		Brief:      "提供DND5E规则TRPG支持",
		Author:     "木落",
		AutoActive: true, // 是否自动开启
		Official:   true,
		ConflictWith: []string{
			"coc7",
		},
		OnCommandReceived: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) {
		},
		GetDescText: GetExtensionDesc,
		CmdMap: CmdMapCls{
			"dnd":  cmdDnd,
			"dndx": cmdDnd,
			"ri":   cmdRi,
			"init": cmdInit,
			// "属性":    cmdSt,
			"st":         cmdSt,
			"dst":        cmdSt,
			"rc":         cmdRc,
			"ra":         cmdRc,
			"rah":        cmdRc,
			"rch":        cmdRc,
			"drc":        cmdRc,
			"buff":       cmdBuff,
			"dbuff":      cmdBuff,
			"spellslots": cmdSpellSlot,
			"ss":         cmdSpellSlot,
			"dss":        cmdSpellSlot,
			"法术位":        cmdSpellSlot,
			"cast":       cmdCast,
			"dcast":      cmdCast,
			"长休":         cmdLongRest,
			"longrest":   cmdLongRest,
			"dlongrest":  cmdLongRest,
			"ds":         cmdDeathSavingThrow,
			"死亡豁免":       cmdDeathSavingThrow,
		},
	}

	self.RegisterExtension(theExt)
}

var dndRiLock sync.Mutex

// LoadByCurGroup 从群信息中加载
func (lst RIList) LoadByCurGroup(ctx *MsgContext) RIList {
	am := ctx.Dice.AttrsManager
	attrs, _ := am.LoadById(ctx.Group.GroupID)

	dndRiLock.Lock()
	riList := attrs.Load("riList")
	if riList == nil || riList.TypeId != ds.VMTypeArray {
		riList = ds.NewArrayVal()
		attrs.Store("riList", riList)
	}
	dndRiLock.Unlock()

	ret := RIList{}
	for _, i := range riList.MustReadArray().List {
		if i.TypeId != ds.VMTypeDict {
			continue
		}

		dd := i.MustReadDictData()
		readStr := func(key string) string {
			v, ok := dd.Dict.Load(key)
			if !ok {
				return ""
			}
			return v.ToString()
		}
		readInt := func(key string) ds.IntType {
			v, ok := dd.Dict.Load(key)
			if !ok {
				return 0
			}
			ret, _ := v.ReadInt()
			return ret
		}

		ret = append(ret, &RIListItem{
			name:   readStr("name"),
			val:    int64(readInt("val")),
			uid:    readStr("uid"),
			detail: readStr("detail"),
		})
	}

	return ret
}

// SaveToGroup 写入群信息中
func (lst RIList) SaveToGroup(ctx *MsgContext) {
	am := ctx.Dice.AttrsManager
	attrs, _ := am.LoadById(ctx.Group.GroupID)
	riList := ds.NewArrayVal()

	ad := riList.MustReadArray()
	for _, i := range lst {
		v := ds.NewDictValWithArrayMust(
			ds.NewStrVal("name"), ds.NewStrVal(i.name),
			ds.NewStrVal("val"), ds.NewIntVal(ds.IntType(i.val)),
			ds.NewStrVal("uid"), ds.NewStrVal(i.uid),
			ds.NewStrVal("detail"), ds.NewStrVal(i.detail),
		)
		ad.List = append(ad.List, v.V())
	}

	dndRiLock.Lock()
	attrs.Store("riList", riList)
	dndRiLock.Unlock()
}

func (lst RIList) GetExists(name string) *RIListItem {
	for _, i := range lst {
		if i.name == name {
			return i
		}
	}
	return nil
}

func setInitNextRoundVars(ctx *MsgContext, lst RIList, round int64) {
	l := len(lst)
	if round == 0 {
		VarSetValueStr(ctx, "$t新轮开始提示", DiceFormatTmpl(ctx, "DND:先攻_新轮开始提示"))
		VarSetValueStr(ctx, "$t当前回合角色名", lst[l-1].name)
		VarSetValueStr(ctx, "$t当前回合at", AtBuild(lst[l-1].uid))
	} else {
		VarSetValueStr(ctx, "$t新轮开始提示", "")
		VarSetValueStr(ctx, "$t当前回合角色名", lst[round-1].name)
		VarSetValueStr(ctx, "$t当前回合at", AtBuild(lst[round-1].uid))
	}
	VarSetValueInt64(ctx, "$g当前回合先攻值", lst[round].val)
	VarSetValueStr(ctx, "$t下一回合角色名", lst[round].name)
	VarSetValueStr(ctx, "$t下一回合at", AtBuild(lst[round].uid))

	nextRound := round + 1
	if l <= int(nextRound) || nextRound < 0 {
		nextRound = 0
	}
	VarSetValueStr(ctx, "$t下下一回合角色名", lst[nextRound].name)
	VarSetValueStr(ctx, "$t下下一回合at", AtBuild(lst[nextRound].uid))
	VarSetValueInt64(ctx, "$g回合数", round)
}
