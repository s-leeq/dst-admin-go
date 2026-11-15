package collect

import (
	"dst-admin-go/internal/database"
	"dst-admin-go/internal/model"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hpcloud/tail"
)

var Collector *Collect

type Collect struct {
	state             chan int
	stop              chan bool
	severLogList      []string
	serverChatLogList []string
	length            int
	clusterName       string
}

func NewCollect(baseLogPath string, clusterName string) *Collect {
	collect := &Collect{
		state: make(chan int, 1),
		severLogList: []string{
			filepath.Join(baseLogPath, "Master", "server_log.txt"),
		},
		serverChatLogList: []string{
			filepath.Join(baseLogPath, "Master", "server_chat_log.txt"),
		},
		stop:        make(chan bool, 2),
		length:      2,
		clusterName: clusterName,
	}
	collect.state <- 1
	return collect
}

func (c *Collect) Stop() {
	close(c.stop)
}

func (c *Collect) ReCollect(baseLogPath, clusterName string) {
	for i := 0; i < c.length; i++ {
		c.stop <- true
	}
	c.severLogList = []string{
		filepath.Join(baseLogPath, "Master", "server_log.txt"),
	}
	c.serverChatLogList = []string{
		filepath.Join(baseLogPath, "Master", "server_chat_log.txt"),
	}
	c.clusterName = clusterName
	c.state <- 1
}

func (c *Collect) StartCollect() {
	go func() {
		for {
			select {
			case <-c.state:
				// 采集
				for _, s := range c.severLogList {
					go c.tailServeLog(s)
				}
				for _, s := range c.serverChatLogList {
					go c.tailServerChatLog(s)
				}
			default:
				time.Sleep(5 * time.Second)
				continue
			}
		}
	}()
}

func (c *Collect) parseSpawnRequestLog(text string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Spawn request Log text: %s\n", text)
			log.Printf("玩家角色日志解析异常: %v\n", r)
		}
	}()

	// 捕获 (1)时间, (2)动作, (3)角色, (4)玩家名
	re := regexp.MustCompile(`^\[([^\]]+)\]:\s*(.*?):\s*(\w+)\s*from\s*(.+)$`)
	matches := re.FindStringSubmatch(text)

	if len(matches) != 5 {
		// 如果日志格式不匹配，直接退出
		log.Printf("Spawn request 日志格式不匹配: %s\n", text)
		return
	}

	t := matches[1] // 00:37:41
	// action := matches[2] // Spawn request
	role := matches[3] // winona
	name := strings.TrimSpace(matches[4])

	spawn := model.Spawn{Name: name, Role: role, Time: t, ClusterName: c.clusterName}
	if err := database.Db.Create(&spawn).Error; err != nil {
		log.Printf("插入玩家 Spawn 日志失败: %v\n", err)
	}
}

func (c *Collect) parseRegenerateLog(text string) {
	defer func() {
		if err := recover(); err != nil {
			log.Println("Generating 日志解析异常:", err)
		}
	}()

	regenerate := model.Regenerate{
		ClusterName: c.clusterName,
	}
	database.Db.Create(&regenerate)
}

func (c *Collect) parseNewIncomingLog(lines []string) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("new incoming 日志解析异常:", r)
		}
	}()

	// DST 762172 的进服日志行序和旧版不同，按行号取字段会全部错位
	// （ip 取到 "1.2.3.4|1"、kuid 取到 "ValidateGameSessionToken" 之类）。
	// 这里改成逐行正则匹配，字段出现顺序变化也不影响。
	reConnected := regexp.MustCompile(`Client connected from ([0-9a-fA-F\.:]+)\|`)
	reAuth := regexp.MustCompile(`Client authenticated: \((KU_[^)]+)\)\s*(.+)$`)
	reInit := regexp.MustCompile(`\[ClientObject\] Initialized .*userid=(KU_\S+)\s+netid=(\d+)`)
	reSession := regexp.MustCompile(`Resuming user: session/\S+/(\S+)`)

	connect := model.Connect{ClusterName: c.clusterName}
	for _, line := range lines {
		if m := reConnected.FindStringSubmatch(line); m != nil {
			connect.Ip = m[1]
		}
		if m := reAuth.FindStringSubmatch(line); m != nil {
			connect.KuId = m[1]
			connect.Name = strings.TrimSpace(m[2])
		}
		if m := reInit.FindStringSubmatch(line); m != nil {
			connect.KuId = m[1]
			connect.SteamId = m[2]
		}
		if m := reSession.FindStringSubmatch(line); m != nil {
			connect.SessionFile = m[1]
		}
	}

	if connect.KuId == "" && connect.Ip == "" {
		log.Println("new incoming 日志未解析到有效字段，跳过:", lines)
		return
	}
	if err := database.Db.Create(&connect).Error; err != nil {
		log.Println("插入玩家连接日志失败:", err)
	}
	log.Println("玩家连接记录:", connect.Name, connect.KuId, connect.SteamId, connect.Ip)
}

// parseSpawnRole 解析「User ID KU_xxx assigned ownership to entity 128734 - willow」，
// 新版服务端没有 "Spawn request: <role> from <name>"，角色信息只在这条日志里。
func (c *Collect) parseSpawnRole(text string) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("角色日志解析异常:", r)
		}
	}()

	re := regexp.MustCompile(`User ID\s+(KU_\S+)\s+assigned ownership to entity\s+\d+ - (\w+)`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return
	}
	kuId, role := m[1], m[2]
	t := ""
	if i := strings.Index(text, "]:"); i > 0 {
		t = strings.TrimPrefix(text[:i], "[")
	}

	connect := new(model.Connect)
	database.Db.Where("ku_id = ? and cluster_name = ?", kuId, c.clusterName).Last(connect)
	name := connect.Name
	if name == "" {
		name = kuId
	}

	spawn := model.Spawn{Name: name, Role: role, Time: t, ClusterName: c.clusterName}
	if err := database.Db.Create(&spawn).Error; err != nil {
		log.Println("插入玩家 Spawn 日志失败:", err)
	}
	log.Println("玩家角色记录:", name, kuId, role)
}

func (c *Collect) tailServeLog(fileName string) {

	log.Println("开始采集 path:", fileName)
	config := tail.Config{
		ReOpen:    true,                                 // 重新打开
		Follow:    true,                                 // 是否跟随
		Location:  &tail.SeekInfo{Offset: 0, Whence: 2}, // 从文件的哪个地方开始读
		MustExist: false,                                // 文件不存在不报错
		Poll:      true,
	}
	tails, err := tail.TailFile(fileName, config)
	if err != nil {
		log.Println("文件监听失败", err)
	}
	var (
		which        = 0
		isNewConnect = false
		incoming     []string
	)
	for {
		select {
		case line, ok := <-tails.Lines:
			if !ok {
				log.Println("文件读取失败", err)
				time.Sleep(time.Second)
			} else {
				text := line.Text
				if find := strings.Contains(text, "assigned ownership to entity"); find {
					c.parseSpawnRole(text)
				} else if find := strings.Contains(text, "Spawn request"); find {
					c.parseSpawnRequestLog(text)
				} else if find := strings.Contains(text, "# Generating"); find {
					c.parseRegenerateLog(text)
				} else if find := strings.Contains(text, "New incoming connection"); find {
					// 上一批还没攒够行数就又来了一个新连接：先把上一批落库，避免两组字段串在一起
					if isNewConnect && len(incoming) > 1 {
						c.parseNewIncomingLog(incoming)
					}
					isNewConnect = true
					incoming = []string{}
					which = 0
				}
				// 获取接下来的五条数据
				if isNewConnect {
					incoming = append(incoming, text)
					which++
					if which > 11 {
						isNewConnect = false
						which = 0
						c.parseNewIncomingLog(incoming)
						incoming = []string{}
					}
				}
			}
		case <-c.stop:
			// 结束监听
			err := tails.Stop()
			if err != nil {
				log.Println("tail log 结束失败")
				return
			}
			return
		}
	}
}

func (c *Collect) parseChatLog(text string) {
	defer func() {
		if err := recover(); err != nil {
			log.Println("玩家行为日志解析异常:", err)
		}
	}()
	//[00:00:55]: [Join Announcement] 猜猜我是谁
	if strings.Contains(text, "[Join Announcement]") {
		c.parseJoin(text)
	}
	//[00:02:28]: [Leave Announcement] 猜猜我是谁
	if strings.Contains(text, "[Leave Announcement]") {
		c.parseLeave(text)
	}
	//[00:02:17]: [Death Announcement] 猜猜我是谁 死于： 采摘的红蘑菇。她变成了可怕的鬼魂！
	if strings.Contains(text, "[Death Announcement]") {
		c.parseDeath(text)
	}
	//[00:02:37]: [Resurrect Announcement] 猜猜我是谁 复活自： TMIP 控制台.
	if strings.Contains(text, "[Resurrect Announcement]") {
		c.parseResurrect(text)
	}
	//[00:03:16]: [Say] (KU_Mt-zrX8K) 猜猜我是谁: 你好啊
	if strings.Contains(text, "[Say]") {
		c.parseSay(text)
	}
	//[10:01:42]: [Announcement] 欢迎访客歪比巴卜，游玩
	if strings.Contains(text, "[Announcement]") {
		c.parseAnnouncement(text)
	}
}

func (c *Collect) parseSay(text string) {
	fmt.Println(text)

	// 正则解析日志
	re := regexp.MustCompile(`\[(.*?)\]: (\[.*?\]) \((.*?)\) (.*?): (.*)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 6 {
		fmt.Println("无法解析日志:", text, matches)
		return
	}

	// 时间
	t := matches[1]
	// [Say]
	action := matches[2]
	kuId := matches[3]
	// 玩家名字，可包含空格
	name := matches[4]
	actionDesc := matches[5]

	// 获取玩家角色和连接信息
	connect := c.getConnectInfo(name)
	spawn := c.getSpawnRole(name, connect.KuId)

	playerLog := model.PlayerLog{
		Name:        name,
		Role:        spawn.Role,
		Action:      action,
		ActionDesc:  actionDesc,
		Time:        t,
		Ip:          connect.Ip,
		KuId:        kuId,
		SteamId:     connect.SteamId,
		ClusterName: c.clusterName,
	}

	// 保存到数据库，并打印错误
	if err := database.Db.Create(&playerLog).Error; err != nil {
		fmt.Println("插入玩家日志失败:", err)
	}
}

func (c *Collect) parseResurrect(text string) {
	c.parseDeath(text)
}

func (c *Collect) parseDeath(text string) {
	fmt.Println(text)

	// 正则表达式 (1)时间, (2)动作, (3)剩余所有内容
	re := regexp.MustCompile(`^\[([^\]]+)\]:\s*(\[[^\]]+\])\s*(.*)$`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 4 {
		log.Println("无法解析 Announcement Log (正则不匹配):", text)
		return
	}

	t := matches[1]
	action := matches[2]
	// 擦屁股
	action = strings.ReplaceAll(action, " ", "")
	rest := strings.TrimSpace(matches[3]) // 名字 + 描述 整体

	var name string
	var actionDesc string

	// 死亡/复活的分隔符列表 (支持中英文)
	// 关键：在 Death Announce 和 Resurrect Announce 之间寻找共同的分隔模式
	// 中文：死于： / 复活自：
	// 英文：died from / resurrected from / revived by
	announcementWords := []string{
		"死于：", "died from", "was killed by", "starved", "suicide", // 死亡
		"复活自：", "resurrected from", "revived by", // 复活
	}

	splitIndex := -1
	for _, word := range announcementWords {
		// 查找分隔符
		idx := strings.Index(rest, word)
		if idx > splitIndex { // 找到最靠前的已知分隔符
			splitIndex = idx
			// 找到后立即退出循环，因为第一个匹配就是名字和描述的边界
			break
		}
	}

	if splitIndex != -1 {
		// 找到了分隔符：分割 rest
		name = strings.TrimSpace(rest[:splitIndex])
		actionDesc = strings.TrimSpace(rest[splitIndex:])
	} else {
		// 未找到已知分隔符，假设整个 rest 都是名字，描述为空 (适用于名字很长，或系统消息)
		name = rest
		actionDesc = ""
		fmt.Println("Announcement Log 未找到分隔符，将全部分配给 Name:", name)
	}

	connect := c.getConnectInfo(name)
	spawn := c.getSpawnRole(name, connect.KuId)
	fmt.Println(connect)

	playerLog := model.PlayerLog{
		Name:        name,
		Role:        spawn.Role,
		Action:      action,
		ActionDesc:  actionDesc,
		Time:        t,
		Ip:          connect.Ip,
		KuId:        connect.KuId,
		SteamId:     connect.SteamId,
		ClusterName: c.clusterName,
	}

	if err := database.Db.Create(&playerLog).Error; err != nil {
		fmt.Println("插入玩家日志失败:", err)
	}
}

func (c *Collect) parseLeave(text string) {
	c.parseJoin(text)
}

func (c *Collect) parseJoin(text string) {
	fmt.Println(text)

	// 正则表达式：捕获 (1)时间, (2)动作, (3)玩家名
	re := regexp.MustCompile(`^\[([^\]]+)\]:\s*(\[[^\]]+\])\s*(.+)$`)
	matches := re.FindStringSubmatch(text)

	// 预期匹配 4 组：[完整匹配, 时间, 动作, 玩家名]
	if len(matches) != 4 {
		log.Println("无法解析 Join Log (正则不匹配):", text)
		return
	}

	// 捕获结果
	t := matches[1]      // 时间: 00:01:43
	action := matches[2] // 动作: [Join Announcement]
	// 擦屁股
	action = strings.ReplaceAll(action, " ", "")
	// 玩家名字是捕获组 3，使用 strings.TrimSpace 确保名字前后没有多余空格
	name := strings.TrimSpace(matches[3])

	connect := c.getConnectInfo(name)
	spawn := c.getSpawnRole(name, connect.KuId)

	playerLog := model.PlayerLog{
		Name:        name,
		Role:        spawn.Role,
		Action:      action,
		Time:        t,
		Ip:          connect.Ip,
		KuId:        connect.KuId,
		SteamId:     connect.SteamId,
		ClusterName: c.clusterName,
	}

	// 保存到数据库，并打印错误
	if err := database.Db.Create(&playerLog).Error; err != nil {
		fmt.Println("插入玩家日志失败:", err)
	}
}

func (c *Collect) tailServerChatLog(fileName string) {
	log.Println("开始采集 path:", fileName)
	config := tail.Config{
		ReOpen:    true,                                 // 重新打开
		Follow:    true,                                 // 是否跟随
		Location:  &tail.SeekInfo{Offset: 0, Whence: 2}, // 从文件的哪个地方开始读
		MustExist: false,                                // 文件不存在不报错
		Poll:      true,
	}
	tails, err := tail.TailFile(fileName, config)
	if err != nil {
		log.Println("文件监听失败", err)
	}
	for {
		select {
		case line, ok := <-tails.Lines:
			if !ok {
				log.Println("文件读取失败", err)
				time.Sleep(time.Second)
			} else {
				text := line.Text
				c.parseChatLog(text)
			}
		case <-c.stop:
			// 结束监听
			err := tails.Stop()
			if err != nil {
				log.Println("tail log 结束失败")
				return
			}
			return
		}
	}
}

func (c *Collect) getSpawnRole(name string, kuId string) *model.Spawn {
	spawn := new(model.Spawn)
	database.Db.Where("name LIKE ? and cluster_name = ?", "%"+name+"%", c.clusterName).Last(spawn)
	if spawn.Role != "" || kuId == "" {
		return spawn
	}
	// 角色行（assigned ownership to entity ... - <role>）可能先于连接记录落库，
	// 那时 spawn 表里的 name 只能是 kuid，所以这里按 kuid 再兜底查一次。
	database.Db.Where("name = ? and cluster_name = ?", kuId, c.clusterName).Last(spawn)
	return spawn
}

func (c *Collect) getConnectInfo(name string) *model.Connect {
	connect := new(model.Connect)
	database.Db.Where("name LIKE ? and cluster_name = ?", "%"+name+"%", c.clusterName).Last(connect)
	return connect
}

func (c *Collect) parseAnnouncement(text string) {
	fmt.Println(text)

	// 正则解析日志
	re := regexp.MustCompile(`\[(.*?)\]: (\[Announcement\]) (.*)`)
	matches := re.FindStringSubmatch(text)
	// 无法解析宣告日志: 00:55:21 Announcement test
	if len(matches) != 4 {
		fmt.Println("无法解析宣告日志:", text, matches)
		return
	}

	// 时间
	t := matches[1]
	// [Announcement]
	action := matches[2]
	// 宣告的内容
	actionDesc := matches[3]

	playerLog := model.PlayerLog{
		Name:        "-",
		Role:        "-",
		Action:      action,
		ActionDesc:  actionDesc,
		Time:        t,
		Ip:          "-",
		KuId:        "-",
		SteamId:     "-",
		ClusterName: c.clusterName,
	}

	// 保存到数据库，并打印错误
	if err := database.Db.Create(&playerLog).Error; err != nil {
		fmt.Println("插入玩家日志失败:", err)
	}
}
