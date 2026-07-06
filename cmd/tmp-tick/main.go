package main

// 临时程序：以真实 save_world_tick 代码路径落第 1 章世界 tick（Fable 亲笔内容，机器护栏）。
// 用完即删。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const args = `{
  "volume": 1,
  "arc": 1,
  "through_chapter": 1,
  "events": [
    {
      "chapter": 1,
      "actors": ["周行舟"],
      "summary": "行舟小超市首夜被困：卷帘门拉不下来，雨衣顾客滞留货架间不付账也不离开，三个躲雨学生留宿店内；周行舟对1704的电话只响半声即断。",
      "consequence": "周行舟撑过首夜后开始清点粗盐、白蜡烛等物资并逐条甄别传闻，『找个守规矩的靠山』的念头成形。",
      "location": "行舟小超市",
      "visibility_chapter": 3,
      "visibility_path": "亲见（电话恢复后自述/带物资上门）",
      "foreshadow_candidate": true,
      "tier": "supporting"
    },
    {
      "chapter": 1,
      "actors": ["黑伞先生", "阴司银行"],
      "summary": "阴司银行旧垣区前台完成冥雾首夜新增账户入册；1704名下出现异常发卡记录（未首刷），被黑伞先生标记为高价值观察账户。",
      "consequence": "『观察-核对-递函』流程启动；江烬的每一笔后续交易都将进入核对序列。",
      "location": "阴司银行旧垣区前台",
      "visibility_chapter": 7,
      "visibility_path": "信使（黑伞先生递名片与首份核对函）",
      "foreshadow_candidate": true,
      "tier": "supporting"
    },
    {
      "chapter": 1,
      "actors": ["唐未晞", "镇厄局雾北行动队"],
      "summary": "分局连夜汇总旧垣区多起『门牌收租』异常报告，行动队集结完毕，3栋所在片区被列入优先封控预案。",
      "consequence": "官方封控力量向旧垣区调动，首次正面介入（3栋封楼线）进入倒计时。",
      "location": "镇厄局雾北分局",
      "visibility_chapter": 4,
      "visibility_path": "官报（封控公告/警戒线目击）",
      "foreshadow_candidate": false,
      "tier": "supporting"
    },
    {
      "chapter": 1,
      "actors": ["蒋牧"],
      "summary": "影子被收走半截后，蒋牧彻夜在1703翻检『还能被认的东西』，三次把1702的奶粉箱挪到门口又搬了回去。",
      "consequence": "补缴执念成形；天亮后他将试探『代缴确认』并向1704求助——成为首个客户样本或首个反面教材的岔口逼近。",
      "location": "阴阳公寓3栋1703",
      "visibility_chapter": 2,
      "visibility_path": "亲见（隔墙动静/次日上门）",
      "foreshadow_candidate": true,
      "tier": "supporting"
    }
  ],
  "agenda_updates": [
    {
      "name": "周行舟",
      "tier": "supporting",
      "current_goal": "撑过被困的首夜后：清点物资、甄别传闻，列出『愿付费求保』街坊名单，第4章前后接上江烬。",
      "motivation": "怕死，但更怕欠人情——他把江烬的话当行情看。",
      "steps": [
        {"description": "首夜守住店面与三个学生，不让雨衣顾客的『赊账』成立", "eta_chapters": 1, "done": true},
        {"description": "清点物资并逐条甄别传闻（粗盐/白蜡烛哪些当真）", "eta_chapters": 2, "done": false},
        {"description": "带物资与街坊名单上3栋找江烬", "eta_chapters": 2, "done": false}
      ],
      "status": "active",
      "last_advanced_chapter": 1
    },
    {
      "name": "蒋牧",
      "tier": "supporting",
      "current_goal": "黎明收雾前补缴失败后，转向『代缴确认』：先把奶粉退烧贴的人情账处理掉，再求1704帮忙。",
      "motivation": "不能接受从『有车有存款的人』跌成『欠租的』；更怕欠1702一条人命。",
      "steps": [
        {"description": "彻夜清点『能被认的东西』——一无所获", "eta_chapters": 1, "done": true},
        {"description": "天亮后送奶粉退烧贴到1702，试探代缴口径", "eta_chapters": 1, "done": false},
        {"description": "向江烬求助补缴/代缴确认", "eta_chapters": 1, "done": false}
      ],
      "status": "active",
      "last_advanced_chapter": 1
    },
    {
      "name": "黑伞先生",
      "tier": "supporting",
      "current_goal": "把1704异常发卡账户纳入观察序列，等待首刷行为，按流程在第7章前后完成首次接触。",
      "motivation": "一个会把消费做成资产的黑卡持有人，是能吃很多年的大账户——他有耐心。",
      "steps": [
        {"description": "旧垣区首夜新增账户入册，标记1704", "eta_chapters": 1, "done": true},
        {"description": "观察首刷行为并起草消费核对函", "eta_chapters": 4, "done": false},
        {"description": "首次接触：递名片与核对函", "eta_chapters": 2, "done": false}
      ],
      "status": "active",
      "last_advanced_chapter": 1
    },
    {
      "name": "江父",
      "tier": "supporting",
      "current_goal": "维持失踪；1701旧债按夜静置计息，死信等待第一个确认权利的人。",
      "motivation": "他消失得越干净，追索越难越过签字落到儿女头上。",
      "status": "dormant",
      "last_advanced_chapter": 1
    }
  ],
  "social_mood": {
    "mood": "首夜过后，恐慌从『不敢承认』转向『偷偷核对』——家家在数自己还剩什么能交，业主群白天异常安静，夜里异常活跃。",
    "intensity": 0.68,
    "rumors": [
      {"text": "12点以后别接门缝里塞的纸，手碰了就算认了账。", "credibility": 0.6, "spread_rate": 0.75, "source_faction": "楼道目击与业主群"},
      {"text": "3栋17楼有人拿整沓现金和金戒指去交租，全被退了，人还折了条腿。", "credibility": 0.5, "spread_rate": 0.65, "source_faction": "隔墙听闻，细节走样"},
      {"text": "有人替整层楼交了租，那层一夜没动静。", "credibility": 0.3, "spread_rate": 0.6, "source_faction": "半句传话（无人能指认哪栋）"},
      {"text": "东门便利店半夜还亮着，进去的人出来时拎的不是自己挑的东西。", "credibility": 0.5, "spread_rate": 0.5, "source_faction": "店门口目击者"},
      {"text": "镇厄局在旧垣区外围拉了警戒线，天亮前进出都要登记。", "credibility": 0.65, "spread_rate": 0.4, "source_faction": "公告残字与警灯目击"}
    ]
  },
  "faction_clock_updates": [
    {"target": "阴司银行", "ticks": 1, "note": "旧垣区首夜建账完成，1704异常发卡入观察名册"},
    {"target": "镇厄局雾北行动队", "ticks": 1, "note": "异常报告汇总，封控预案圈定3栋片区"},
    {"target": "阴阳公寓夜租规则", "ticks": 1, "note": "首夜收租执行完毕，欠费链开始计息，头七对账夜进入倒数"}
  ]
}`

func main() {
	st := store.NewStore("data/runs/鬼城/output/novel")
	tool := tools.NewSaveWorldTickTool(st)
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	var pretty any
	_ = json.Unmarshal(out, &pretty)
	b, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(b))
}
