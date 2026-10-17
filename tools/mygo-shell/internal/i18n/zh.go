package i18n

// chinese is the simplified Chinese table.
//
// The wording follows the desktop host's own Strings.zh.json where the same
// thing appears in both, so a person who has used one recognises the other:
// "正在播放", "正在连接", "实时 · 120 ms" are its words, not new ones.
var chinese = map[string]string{
	// Navigation.
	"AirFlash":  "AirFlash",
	"Stats":     "状态",
	"Receivers": "设备",
	"Settings":  "设置",

	// Phases.
	"Idle":              "空闲",
	"Playing":           "正在播放",
	"Stopped":           "已停止",
	"Failed":            "失败",
	"Connecting":        "正在连接",
	"Negotiating":       "正在协商",
	"connecting…":       "正在连接…",
	"could not connect": "无法连接",
	"no reading yet":    "还没有读数",

	// Receivers.
	"Looking for receivers…":                                 "正在查找设备…",
	"The PC and the HomePod have to be on the same network.": "电脑和 HomePod 需要在同一个网络。",
	"offline":                  "离线",
	"in use by another device": "正被其他设备占用",
	"not answering":            "没有响应",
	"free":                     "空闲",
	"pair incomplete":          "配对不完整",
	"Play":                     "播放",
	"Stop":                     "停止",
	"VOL":                      "音量",

	// Readings.
	"ms delay":             "ms 延迟",
	"waiting for playback": "等待播放",
	"nothing measured yet": "还没有测量数据",

	// The charts.
	"LATENCY OVER TIME":     "延迟曲线",
	"GAPS":                  "断音",
	"VOLUME":                "音量",
	"CAPTURE":               "采集",
	"LOSSES":                "丢包",
	"LATENCY":               "延迟",
	"none in the last %d s": "最近 %d 秒没有断音",
	"%d of %d s lost audio": "%d / %d 秒有断音",
	"One mark per second of playback appears here. Nothing has been lost so far.": "播放的每一秒会有一个标记。目前没有丢音。",
	"The measured latency appears here once playback starts.":                     "开始播放后会显示测得的延迟。",
	"s ago": "秒前",
	"now":   "现在",

	// Settings.
	"Lightweight mode":           "轻量模式",
	"Start when I sign in":       "登录时启动",
	"Reconnect a dropped stream": "断流后重连",
	"Closing the window":         "关闭窗口",
	"Attempts":                   "重试次数",
	"System default":             "跟随系统",
	"English":                    "English",

	// The capture section.
	"the PC's own audio is faltering": "电脑采集的音频有问题",
	"sound is being captured":         "正在采集音频",

	// The loss section.
	"something is being lost":   "有丢包",
	"nothing lost so far":       "没有丢包",
	"Sender fell behind":        "发送端落后",
	"Audio slots skipped":       "音频槽被跳过",
	"Lost past recovery":        "已无法恢复",
	"Packets dropped on send":   "发送时丢弃",
	"Retransmit queue overflow": "重传队列溢出",

	// Latency modes, as the desktop names them.
	"Custom":                             "自定义",
	"Auto":                               "自动",
	"The engine will be asked for %d ms": "引擎将使用 %d ms",
	" · Auto, at your floor":             " · 自动，按你的下限",
	" · Auto raised it from your floor of %d ms": " · 自动从你的下限 %d ms 上调",
	"A new target applies to the next start.":    "新目标在下次开始播放时生效。",

	// The adapter the receiver search runs on.
	"Automatic": "自动",
	"Adapter":   "网卡",
	"NETWORK":   "网络",
	"The adapter the receiver search runs on. A PC with a VPN up can find a HomePod on one adapter only.": "接收端搜索运行在哪块网卡上。挂了 VPN 的电脑只能在其中一块网卡上找到 HomePod。",
	"A new adapter takes effect the next time the search runs.":                                           "新网卡在下次搜索时生效。",

	// Silencing the local speakers while streaming.
	"Mute the PC speakers while streaming": "播放时静音电脑扬声器",
	"The sound is being sent to the HomePod, so playing it here too would be the same voice twice, one delayed behind the other.": "声音正在发送到 HomePod，本地再播一遍就是同一个声音两次，其中一次还会慢半拍。",

	// Audio input.
	"AUDIO INPUT":  "音频输入",
	"Input device": "输入设备",
	"The sound the PC is playing is recorded from this device. A machine with one sound card has nothing to choose.": "电脑播放的声音从这个设备录制。只有一块声卡的电脑没有可选的。",
	"A new device applies to the next stream.": "新设备在下次开始播放时生效。",

	// Launch behaviour.
	"Show the window when I open the app": "手动打开时显示窗口",
	"Off by default: the app starts in the tray and opens its panel. Starting with the machine always stays quiet, here or not.": "默认关闭：程序启动后驻留托盘并弹出面板。开机自启始终静默，与此设置无关。",

	// Pairing.
	"Pairing": "配对",
	"Pair":    "配对",
	"Pair this receiver with this PC. A receiver already paired does not need it again.": "把这台设备和这台电脑配对。已经配对过的设备不需要再来一次。",
	"Enter the PIN shown on %s": "请输入 %s 上显示的 PIN",
	"The PIN is on the HomePod's own screen, under the speaker's name. The engine waits for a few minutes.": "PIN 显示在 HomePod 自己的屏幕上，位于音箱名称下方。引擎会等几分钟。",

	// Manual receivers and aliases.
	"Add one by hand": "手动添加",
	"Remove":          "移除",
	"IP address":      "IP 地址",
	"Port":            "端口",
	"Name":            "名称",
	"Add":             "添加",
	"Cancel":          "取消",
	"NAME":            "名称",
	"For a receiver the network search cannot see: a speaker on another subnet, or one whose announcements the router does not forward.": "用于网络搜索看不到的设备：位于其他子网的扬声器，或路由器不转发其广播的设备。",

	// The long descriptions on the settings page. Each is one whole sentence,
	// which is what makes it one key: a sentence split across lines for the
	// source is joined again by the Go compiler before it reaches the table.
	"Drops the window material and the trend chart, and samples the engine once a second. The audio path is untouched: the engine keeps its own process either way.":                                                                               "去掉窗口材质和延迟曲线，每秒只采样一次。音频链路不受影响：引擎始终在自己的进程里。",
	"The shell starts in the tray at sign-in and connects to the last receiver, so nothing has to be opened.":                                                                                                                                      "登录时自动在托盘启动并连接上次的设备，不需要手动打开。",
	"When the connection drops, start it again. Each retry waits twice as long as the one before, up to eight seconds, and a stream that ran for ten seconds starts the count over. A HomePod that asks for a PIN is not retried: that needs you.": "连接断开时自动重连。每次重试的等待是上一次的两倍，最长八秒；持续播放十秒以上的会话会把次数重新算起。需要配对码的 HomePod 不会重试，那需要你确认。",
	"Closing releases the window and everything it holds. Playback continues: the engine is a separate process, and the app stays in the tray.":                                                                                                    "关闭窗口会释放窗口及它占用的一切。播放继续：引擎是独立进程，程序仍留在托盘。",
	"Quit":                          "退出",
	"well over target":              "远超目标",
	"slightly over target":          "略高于目标",
	"on target":                     "符合目标",
	"waiting for the first reading": "等待第一次读数",
	"Use global setting":            "跟随全局",
	"its own":                       "本设备设置",
	"global":                        "全局",
	"Latency %d ms":                 "延迟 %d ms",
	"Tune":                          "单设备设置",
	"Equalizer":                     "均衡器",
	"Preamp":                        "预放大",
	"Flat":                          "平直",
	"Bass boost":                    "低音增强",
	"Vocals":                        "人声",
	"Pop":                           "流行",
	"Rock":                          "摇滚",
	"Ten bands from 31 Hz to 16 kHz, each moving up to 12 dB. A change is heard as it is made.": "从 31 Hz 到 16 kHz 共十段，每段最多 ±12 dB。改动会立即生效。",
	"Target":                                 "目标",
	"Receiver buffer":                        "接收端缓冲",
	"Network round trip":                     "网络往返",
	"Real-time · 120 ms":                     "实时 · 120 ms",
	"Normal · 200 ms":                        "正常 · 200 ms",
	"Buffered · 500 ms":                      "缓冲 · 500 ms",
	"Input rate":                             "输入采样率",
	"Capture to send, slowest 5%":            "采集到发送（最慢 5%）",
	"Queue age":                              "队列积压",
	"Frames dropped":                         "丢帧",
	"Buffer underruns":                       "缓冲欠载",
	"SAMPLE RATE":                            "采样率",
	"A new rate applies to the next stream.": "新采样率在下次开始播放时生效。",
	"44.1 kHz":                               "44.1 kHz",
	"48 kHz":                                 "48 kHz",
	"Settings could not be saved: %v":        "设置无法保存：%v",
}
