package cronsun

import (
	"reflect"
	"testing"
)

func TestJob_splitCmd(t *testing.T) {
	tests := []struct {
		name    string   // 测试名称
		command string   // 输入命令
		wantCmd []string // 期望的结果
	}{
		// 测试用例1：只有命令，没有参数
		{
			name:    "command without arguments",
			command: "ls",
			wantCmd: []string{"ls"},
		},
		// 测试用例2：命令和单个参数
		{
			name:    "command with single argument",
			command: "echo hello",
			wantCmd: []string{"echo", "hello"},
		},
		// 测试用例3：命令和多个参数
		{
			name:    "command with multiple arguments",
			command: "grep -i 'error' file.log",
			wantCmd: []string{"grep", "-i", "error", "file.log"},
		},
		// 测试用例4：空命令
		{
			name:    "empty command",
			command: "",
			wantCmd: []string{""}, // 因为命令是空的，应该返回一个空字符串切片
		},
	}

	// 遍历测试用例
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 初始化 Job 实例
			j := &Job{
				Command: tt.command,
			}

			// 调用要测试的函数
			j.splitCmd()

			// 比较期望结果和实际结果
			if !reflect.DeepEqual(j.cmd, tt.wantCmd) {
				t.Errorf("splitCmd() = %v, want %v", j.cmd, tt.wantCmd)
			}
		})
	}
}
