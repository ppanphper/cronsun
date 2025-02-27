package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	client "github.com/coreos/etcd/clientv3"
	"github.com/robfig/cron/v3"
	"github.com/shunfei/cronsun"
	"github.com/shunfei/cronsun/conf"
	"github.com/shunfei/cronsun/log"
	"github.com/shunfei/cronsun/utils"
)

// Node 执行 cron 命令服务的结构体
type Node struct {
	*cronsun.Client
	*cronsun.Node
	*cron.Cron

	// key 为 cmd 的 ID（cmd.GetID），值为 cron 的 entry ID
	// 可通过获取 entry ID，来删除这个 cron 任务
	cronEntryIDIndex map[string]cron.EntryID

	jobs   Jobs // 和结点相关的任务
	groups Groups
	cmds   map[string]*cronsun.Cmd

	link
	// 删除的 job id，用于 group 更新
	delIDs map[string]bool

	ttl  int64
	lID  client.LeaseID // lease id
	done chan struct{}
}

func NewNode(cfg *conf.Conf) (n *Node, err error) {
	uuid, err := cfg.UUID()
	if err != nil {
		return
	}

	ip, err := utils.LocalIP()
	if err != nil {
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid
		err = nil
	}

	n = &Node{
		Client: cronsun.DefalutClient,
		Node: &cronsun.Node{
			ID:       uuid,
			PID:      strconv.Itoa(os.Getpid()),
			PIDFile:  strings.TrimSpace(cfg.PIDFile),
			IP:       ip.String(),
			Hostname: hostname,
		},
		Cron: cron.New(cron.WithSeconds()),

		jobs: make(Jobs, 8),
		cmds: make(map[string]*cronsun.Cmd),

		link:   newLink(8),
		delIDs: make(map[string]bool, 8),

		ttl:  cfg.Ttl,
		done: make(chan struct{}),
	}
	return
}

// 注册到 /cronsun/node/xx
func (n *Node) Register() (err error) {
	pid, err := n.Node.Exist()
	if err != nil {
		return
	}

	if pid != -1 {
		return fmt.Errorf("node[%s] pid[%d] exist", n.Node.ID, pid)
	}

	return n.set()
}

func (n *Node) set() error {
	resp, err := n.Client.Grant(n.ttl + 2)
	if err != nil {
		return err
	}

	if _, err = n.Node.Put(client.WithLease(resp.ID)); err != nil {
		return err
	}

	n.lID = resp.ID
	n.writePIDFile()

	return nil
}

func (n *Node) writePIDFile() {
	if len(n.PIDFile) == 0 {
		return
	}

	filename := "cronnode_pid"
	if !strings.HasSuffix(n.PIDFile, "/") {
		filename = path.Base(n.PIDFile)
	}

	dir := path.Dir(n.PIDFile)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		log.Errorf("Failed to write pid file: %s. you can change PIDFile config in base.json", err)
		return
	}

	n.PIDFile = path.Join(dir, filename)
	err = os.WriteFile(n.PIDFile, []byte(n.PID), 0644)
	if err != nil {
		log.Errorf("Failed to write pid file: %s. you can change PIDFile config in base.json", err)
		return
	}
}

func (n *Node) removePIDFile() {
	if len(n.PIDFile) == 0 {
		return
	}

	if err := os.Remove(n.PIDFile); err != nil {
		log.Warnf("Failed to remove pid file: %s", err)
	}
}

// 断网掉线重新注册
func (n *Node) keepAlive() {
	duration := time.Duration(n.ttl) * time.Second
	timer := time.NewTimer(duration)
	for {
		select {
		case <-n.done:
			return
		case <-timer.C:
			if n.lID > 0 {
				_, err := n.Client.KeepAliveOnce(n.lID)
				if err == nil {
					timer.Reset(duration)
					continue
				}

				log.Warnf("%s lid[%x] keepAlive err: %s, try to reset...", n.String(), n.lID, err.Error())
				n.lID = 0
			}

			if err := n.set(); err != nil {
				log.Warnf("%s set lid err: %s, try to reset after %d seconds...", n.String(), err.Error(), n.ttl)
			} else {
				log.Infof("%s set lid[%x] success", n.String(), n.lID)
			}
			timer.Reset(duration)
		}
	}
}

func (n *Node) loadJobs() (err error) {
	if n.groups, err = cronsun.GetGroups(""); err != nil {
		return
	}

	jobs, err := cronsun.GetJobs()
	if err != nil {
		return
	}

	if len(jobs) == 0 {
		return
	}

	for _, job := range jobs {
		job.Init(n.ID, n.Hostname, n.IP)
		n.addJob(job, false)
	}

	return
}

func (n *Node) addJob(job *cronsun.Job, notice bool) {
	n.link.addJob(job)

	if job.IsRunOn(n.ID, n.groups) {
		n.jobs[job.ID] = job
	}

	cmds := job.Cmds(n.ID, n.groups)
	if len(cmds) == 0 {
		return
	}

	for _, cmd := range cmds {
		n.addCmd(cmd, notice)
	}
	return
}

func (n *Node) delJob(id string) {
	n.delIDs[id] = true
	job, ok := n.jobs[id]
	// 之前此任务没有在当前结点执行
	if !ok {
		return
	}

	delete(n.jobs, id)
	n.link.delJob(job)

	cmds := job.Cmds(n.ID, n.groups)
	if len(cmds) == 0 {
		return
	}

	for _, cmd := range cmds {
		n.delCmd(cmd)
	}
	return
}

func (n *Node) modJob(job *cronsun.Job) {
	oJob, ok := n.jobs[job.ID]
	// 之前此任务没有在当前结点执行，直接增加任务
	if !ok {
		n.addJob(job, true)
		return
	}

	n.link.delJob(oJob)
	prevCmds := oJob.Cmds(n.ID, n.groups)

	job.Count = oJob.Count
	*oJob = *job
	cmds := oJob.Cmds(n.ID, n.groups)

	for id, cmd := range cmds {
		n.modCmd(cmd, true)
		delete(prevCmds, id)
	}

	for _, cmd := range prevCmds {
		n.delCmd(cmd)
	}

	n.link.addJob(oJob)
}

func (n *Node) addCmd(cmd *cronsun.Cmd, notice bool) {
	cmdID := cmd.GetID()

	// 检查命令是否已存在，防止重复添加
	if _, exists := n.cmds[cmdID]; exists {
		log.Warnf("命令 ID %s 已存在，跳过添加", cmdID)
		return
	}

	cronEntryID := n.Cron.Schedule(cmd.JobRule.Schedule, cmd)

	// 添加到内部映射中
	n.cronEntryIDIndex[cmdID] = cronEntryID
	n.cmds[cmdID] = cmd

	if notice {
		log.Infof("job[%s] group[%s] rule[%s] timer[%s] has added", cmd.Job.ID, cmd.Job.Group, cmd.JobRule.ID, cmd.JobRule.Timer)
	}
	return
}

// modCmd 修改节点中的命令信息
func (n *Node) modCmd(cmd *cronsun.Cmd, notice bool) {
	cmdID := cmd.GetID()

	// 检查节点的命令映射中是否已存在该命令ID
	c, ok := n.cmds[cmd.GetID()]
	if !ok {
		// 如果不存在，则调用addCmd函数添加新命令
		n.addCmd(cmd, notice)
		return
	}

	oldSchedule := c.JobRule.Timer
	*c = *cmd

	// 只有节点的执行时间改变，才更新 cron
	if c.JobRule.Timer != oldSchedule {
		cronEntryID := n.cronEntryIDIndex[cmdID]

		// 更新 cron 调度规则
		n.Cron.Remove(cronEntryID)
		newCronEntryID := n.Cron.Schedule(c.JobRule.Schedule, c)
		n.cronEntryIDIndex[cmdID] = newCronEntryID
	}

	if notice {
		log.Infof("job[%s] group[%s] rule[%s] timer[%s] has updated", c.Job.ID, c.Job.Group, c.JobRule.ID, c.JobRule.Timer)
	}
}

// delCmd 删除一个命令（cmd）和其相关的定时任务（cron）。
func (n *Node) delCmd(cmd *cronsun.Cmd) {
	cmdID := cmd.GetID()
	if _, ok := n.cmds[cmdID]; !ok {
		log.Warnf("cmd with ID %s does not exist", cmdID)
		return
	}

	delete(n.cmds, cmdID)

	cronEntryID, ok := n.cronEntryIDIndex[cmdID]
	if !ok {
		log.Warnf("cron entry ID for cmd %s does not exist", cmdID)
		return
	}

	n.Cron.Remove(cronEntryID)

	delete(n.cronEntryIDIndex, cmdID)

	log.Infof("job[%s] group[%s] rule[%s] timer[%s] has deleted",
		cmd.Job.ID, cmd.Job.Group, cmd.JobRule.ID, cmd.JobRule.Timer)
}

func (n *Node) addGroup(g *cronsun.Group) {
	n.groups[g.ID] = g
}

func (n *Node) delGroup(id string) {
	// delete job first
	defer n.link.delGroup(id)
	defer delete(n.groups, id)

	jobLinks := n.link[id]
	if len(jobLinks) == 0 {
		return
	}

	for jID := range jobLinks {
		job, ok := n.jobs[jID]
		// 之前此任务没有在当前结点执行
		if !ok {
			continue
		}

		cmds := job.Cmds(n.ID, n.groups)
		if len(cmds) == 0 {
			continue
		}

		for _, cmd := range cmds {
			n.delCmd(cmd)
		}
	}

	return
}

func (n *Node) modGroup(g *cronsun.Group) {
	oGroup, ok := n.groups[g.ID]
	if !ok {
		n.addGroup(g)
		return
	}

	// 都包含/都不包含当前节点，对当前节点任务无影响
	if (oGroup.Included(n.ID) && g.Included(n.ID)) || (!oGroup.Included(n.ID) && !g.Included(n.ID)) {
		*oGroup = *g
		return
	}

	// 增加当前节点
	if !oGroup.Included(n.ID) && g.Included(n.ID) {
		n.groupAddNode(g)
		return
	}

	// 移除当前节点
	n.groupRmNode(g, oGroup)
	return
}

func (n *Node) groupAddNode(g *cronsun.Group) {
	n.groups[g.ID] = g
	jls := n.link[g.ID]
	if len(jls) == 0 {
		return
	}

	var err error
	for jid, jl := range jls {
		job, ok := n.jobs[jid]
		if !ok {
			// job 已删除
			if n.delIDs[jid] {
				n.link.delGroupJob(g.ID, jid)
				continue
			}

			if job, err = cronsun.GetJob(jl.gname, jid); err != nil {
				log.Warnf("get job[%s][%s] err: %s", jl.gname, jid, err.Error())
				n.link.delGroupJob(g.ID, jid)
				continue
			}

			if err = job.Valid(); err != nil {
				log.Warnf("invalid job[%s][%s]: %s", jl.gname, jid, err.Error())
				n.link.delGroupJob(g.ID, jid)
				continue
			}

			job.Init(n.ID, n.Hostname, n.IP)
			n.jobs[jid] = job
		}

		cmds := job.Cmds(n.ID, n.groups)
		for _, cmd := range cmds {
			n.addCmd(cmd, true)
		}
	}
	return
}

func (n *Node) groupRmNode(g, og *cronsun.Group) {
	jls := n.link[g.ID]
	if len(jls) == 0 {
		n.groups[g.ID] = g
		return
	}

	for jid := range jls {
		job, ok := n.jobs[jid]
		// 之前此任务没有在当前结点执行
		if !ok {
			n.link.delGroupJob(g.ID, jid)
			continue
		}

		n.groups[og.ID] = og
		prevCmds := job.Cmds(n.ID, n.groups)
		n.groups[g.ID] = g
		cmds := job.Cmds(n.ID, n.groups)

		for id, cmd := range cmds {
			n.addCmd(cmd, true)
			delete(prevCmds, id)
		}

		for _, cmd := range prevCmds {
			n.delCmd(cmd)
		}
	}

	n.groups[g.ID] = g
}

func (n *Node) KillExcutingProc(process *cronsun.Process) {
	pid, _ := strconv.Atoi(process.ID)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		log.Warnf("process:[%d] force kill failed, error:[%s]\n", pid, err)
		return
	}
}

func (n *Node) watchJobs() {
	rch := cronsun.WatchJobs()

	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch {
			case ev.IsCreate():
				job, err := cronsun.GetJobFromKv(ev.Kv.Key, ev.Kv.Value)
				if err != nil {
					log.Warnf("err: %s, kv: %s", err.Error(), ev.Kv.String())
					continue
				}

				job.Init(n.ID, n.Hostname, n.IP)
				n.addJob(job, true)
			case ev.IsModify():
				job, err := cronsun.GetJobFromKv(ev.Kv.Key, ev.Kv.Value)
				if err != nil {
					log.Warnf("err: %s, kv: %s", err.Error(), ev.Kv.String())
					continue
				}

				job.Init(n.ID, n.Hostname, n.IP)
				n.modJob(job)
			case ev.Type == client.EventTypeDelete:
				n.delJob(cronsun.GetIDFromKey(string(ev.Kv.Key)))
			default:
				log.Warnf("unknown event type[%v] from job[%s]", ev.Type, string(ev.Kv.Key))
			}
		}
	}
}

func (n *Node) watchExecutingProc() {
	rch := cronsun.WatchProcs(n.ID)

	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch {
			case ev.IsModify():
				key := string(ev.Kv.Key)
				process, err := cronsun.GetProcFromKey(key)
				if err != nil {
					log.Warnf("err: %s, kv: %s", err.Error(), ev.Kv.String())
					continue
				}

				val := string(ev.Kv.Value)
				pv := &cronsun.ProcessVal{}
				err = json.Unmarshal([]byte(val), pv)
				if err != nil {
					continue
				}
				process.ProcessVal = *pv
				if process.Killed {
					n.KillExcutingProc(process)
				}
			}
		}
	}
}

func (n *Node) watchGroups() {
	rch := cronsun.WatchGroups()
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch {
			case ev.IsCreate():
				g, err := cronsun.GetGroupFromKv(ev.Kv.Key, ev.Kv.Value)
				if err != nil {
					log.Warnf("err: %s, kv: %s", err.Error(), ev.Kv.String())
					continue
				}

				n.addGroup(g)
			case ev.IsModify():
				g, err := cronsun.GetGroupFromKv(ev.Kv.Key, ev.Kv.Value)
				if err != nil {
					log.Warnf("err: %s, kv: %s", err.Error(), ev.Kv.String())
					continue
				}

				n.modGroup(g)
			case ev.Type == client.EventTypeDelete:
				n.delGroup(cronsun.GetIDFromKey(string(ev.Kv.Key)))
			default:
				log.Warnf("unknown event type[%v] from group[%s]", ev.Type, string(ev.Kv.Key))
			}
		}
	}
}

func (n *Node) watchOnce() {
	rch := cronsun.WatchOnce()
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch {
			case ev.IsCreate(), ev.IsModify():
				if len(ev.Kv.Value) != 0 && string(ev.Kv.Value) != n.ID {
					continue
				}

				job, ok := n.jobs[cronsun.GetIDFromKey(string(ev.Kv.Key))]
				if !ok || !job.IsRunOn(n.ID, n.groups) {
					continue
				}

				go job.RunWithRecovery()
			}
		}
	}
}

func (n *Node) watchCsctl() {
	rch := cronsun.WatchCsctl()
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch {
			case ev.IsCreate(), ev.IsModify():
				n.executCsctlCmd(ev.Kv.Key, ev.Kv.Value)
			}
		}
	}
}

// Run 启动服务
func (n *Node) Run() (err error) {
	go n.keepAlive()
	defer func() {
		if err != nil {
			n.Stop(nil)
		}
	}()

	if err = n.loadJobs(); err != nil {
		return
	}

	n.Cron.Start()

	go n.startWatchers()

	n.Node.On()
	return
}

func (n *Node) startWatchers() {
	go n.watchJobs()
	go n.watchExecutingProc()
	go n.watchGroups()
	go n.watchOnce()
	go n.watchCsctl()
}

// 停止服务
func (n *Node) Stop(i interface{}) {
	n.Node.Down()
	close(n.done)
	n.Node.Del()
	n.Client.Close()
	n.Cron.Stop()
	n.removePIDFile()
}
