package node

import (
	"github.com/robfig/cron/v3"
	"github.com/shunfei/cronsun"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestNode_addCmd(t *testing.T) {
	// 创建一个虚拟的 Node 对象
	n := &Node{
		Cron:             cron.New(cron.WithSeconds()),
		cronEntryIDIndex: make(map[string]cron.EntryID),
		cmds:             make(map[string]*cronsun.Cmd),
	}

	// 创建一个虚拟的 cronsun.Cmd 对象
	cmd := &cronsun.Cmd{
		Job: &cronsun.Job{
			ID:    "job1",
			Group: "group1",
		},
		JobRule: &cronsun.JobRule{
			ID:    "rule1",
			Timer: "0 * * * * *", // 每分钟执行一次
		},
	}

	// 调用 addCmd 方法
	n.addCmd(cmd, true)

	// 检查 cmd 是否正确添加到 Node 中
	if _, exists := n.cronEntryIDIndex[cmd.GetID()]; !exists {
		t.Errorf("Expected cron entry ID to be added, but it wasn't")
	}

	if addedCmd, exists := n.cmds[cmd.GetID()]; !exists || addedCmd != cmd {
		t.Errorf("Expected cmd to be added to cmds map, but it wasn't")
	}
}
func TestNode_delCmd(t *testing.T) {
	n := &Node{
		Cron:             cron.New(cron.WithSeconds()),
		cmds:             make(map[string]*cronsun.Cmd),
		cronEntryIDIndex: make(map[string]cron.EntryID),
	}

	cmd := &cronsun.Cmd{
		Job: &cronsun.Job{
			ID:    "job1",
			Group: "group1",
		},
		JobRule: &cronsun.JobRule{
			ID:    "rule1",
			Timer: "0 * * * * *", // 每分钟执行一次
		},
	}

	// 将命令添加到 Node 中
	n.addCmd(cmd, false)

	n.delCmd(cmd)

	// 断言该命令已从 Node 的 cmds 中删除
	_, exists := n.cmds[cmd.GetID()]
	assert.False(t, exists, "该命令应该从 Node 中被删除")

	// 断言 cronEntryIDIndex 中的相关 entry 也被删除
	_, exists = n.cronEntryIDIndex[cmd.GetID()]
	assert.False(t, exists, "cron entry ID 应该被删除")
}
func TestNode_modCmd(t *testing.T) {
	// 创建一个虚拟的 Node 对象
	n := &Node{
		Cron:             cron.New(cron.WithSeconds()),
		cronEntryIDIndex: make(map[string]cron.EntryID),
		cmds:             make(map[string]*cronsun.Cmd),
	}

	// 创建一个虚拟的 cronsun.Cmd 对象 (原始命令)
	originalCmd := &cronsun.Cmd{
		Job: &cronsun.Job{
			ID:    "job1",
			Group: "group1",
		},
		JobRule: &cronsun.JobRule{
			ID:    "rule1",
			Timer: "0 * * * * *", // 每分钟执行一次
		},
	}

	// 将命令添加到 Node 中
	n.addCmd(originalCmd, false)

	// 情况 1：测试 modCmd 当命令不存在时，调用 addCmd
	newCmd := &cronsun.Cmd{
		Job: &cronsun.Job{
			ID:    "job2", // 新的命令 ID
			Group: "group1",
		},
		JobRule: &cronsun.JobRule{
			ID:       "rule2",
			Timer:    "0 * * * * *",
			Schedule: cron.Every(1 * time.Minute),
		},
	}

	n.modCmd(newCmd, false) // 调用 modCmd

	// 检查命令是否被添加
	if _, exists := n.cmds[newCmd.GetID()]; !exists {
		t.Errorf("Expected new cmd to be added, but it wasn't")
	}

	// 情况 2：测试 modCmd 当命令存在且时间未变化时，不应删除任务
	n.modCmd(originalCmd, false)

	// 检查命令仍然存在
	if _, exists := n.cmds[originalCmd.GetID()]; !exists {
		t.Errorf("Expected original cmd to still exist, but it was removed")
	}

	// 情况 3：测试 modCmd 当命令存在且时间改变时，删除旧任务并添加新任务
	updatedCmd := &cronsun.Cmd{
		Job: &cronsun.Job{
			ID:    "job1", // 和原来的 job1 相同
			Group: "group1",
		},
		JobRule: &cronsun.JobRule{
			ID:    "rule1",
			Timer: "5 * * * * *",
		},
	}

	// 说明：job id 和 group id 不变的情况下，cmd.GetID 是同一个值
	oldEntryID, exists := n.cronEntryIDIndex[originalCmd.GetID()]
	assert.True(t, exists)

	n.modCmd(updatedCmd, false)

	// 检查旧的 cron 任务是否被删除并且新的任务被添加
	newEntryID, exists := n.cronEntryIDIndex[updatedCmd.GetID()]
	assert.True(t, exists)

	assert.NotEqual(t, t, oldEntryID, newEntryID)
}
