package ai

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestClassifyCommandRiskDeterministic 验证确定性命令风险分级(护城河下限,完全离线、不依赖 AI)。
func TestClassifyCommandRiskDeterministic(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		// danger:破坏性 / 不可逆。
		{"rm -rf /", CmdRiskDanger},
		{"rm -rf /var/lib", CmdRiskDanger},
		{"rm -fr ./*", CmdRiskDanger},
		{"sudo rm -rf $HOME/", CmdRiskDanger},
		{"mkfs.ext4 /dev/sdb1", CmdRiskDanger},
		{"dd if=/dev/zero of=/dev/sda bs=1M", CmdRiskDanger},
		{"echo x > /dev/sda", CmdRiskDanger},
		{":(){ :|:& };:", CmdRiskDanger},
		{"chmod -R 777 /app", CmdRiskDanger},
		{"chmod 777 /etc/passwd", CmdRiskDanger},
		{"find /var/log -name '*.log' -mtime +7 -delete", CmdRiskDanger},
		{"shutdown -h now", CmdRiskDanger},
		{"reboot", CmdRiskDanger},
		{"kill -9 -1", CmdRiskDanger},
		{"crontab -r", CmdRiskDanger},
		{"git reset --hard origin/main", CmdRiskDanger},
		// write:会修改文件 / 服务 / 状态。
		{"sed -i 's/a/b/' conf.yaml", CmdRiskWrite},
		{"systemctl restart nginx", CmdRiskWrite},
		{"nginx -s reload", CmdRiskWrite},
		{"echo hi > out.txt", CmdRiskWrite},
		{"mv a.txt b.txt", CmdRiskWrite},
		{"apt-get install -y curl", CmdRiskWrite},
		{"docker restart app", CmdRiskWrite},
		{"kubectl apply -f deploy.yaml", CmdRiskWrite},
		{"kill 1234", CmdRiskWrite},
		{"mkdir -p /data/new", CmdRiskWrite},
		// safe:只读 / 查询。
		{"ls -la", CmdRiskSafe},
		{"ps aux --sort=-%mem | head -6", CmdRiskSafe},
		{"cat /etc/os-release", CmdRiskSafe},
		{"df -h", CmdRiskSafe},
		{"du -ah . | sort -rh | head -10", CmdRiskSafe},
		{"tail -f /app/logs/app.log", CmdRiskSafe},
		{"lsof -i :8080 -sTCP:LISTEN", CmdRiskSafe},
		{"grep ERROR app.log", CmdRiskSafe},
		{"", CmdRiskSafe},
	}
	for _, c := range cases {
		got, _ := classifyCommandRisk(c.cmd)
		if got != c.want {
			t.Errorf("classifyCommandRisk(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

// TestReconcileRiskEscalates 验证 AI 判 safe 但命令实为危险时,确定性复核无条件升级到 danger。
func TestReconcileRiskEscalates(t *testing.T) {
	// AI 误判 safe 的 rm -rf。
	risk, reason := reconcileRisk(CmdRiskSafe, "查看日志", "rm -rf /tmp/*")
	if risk != CmdRiskDanger {
		t.Fatalf("确定性应把 rm -rf 升级为 danger,得 %q", risk)
	}
	if !strings.Contains(reason, "递归") {
		t.Fatalf("升级时应前置确定性理由,得 %q", reason)
	}
	// AI 判 danger 而命令实际只读:取更危险者(尊重 AI 的更高判断)。
	risk2, _ := reconcileRisk(CmdRiskDanger, "谨慎", "ls -la")
	if risk2 != CmdRiskDanger {
		t.Fatalf("应取更危险者 danger,得 %q", risk2)
	}
	// 都为 safe:保持 safe。
	risk3, _ := reconcileRisk(CmdRiskSafe, "", "cat file")
	if risk3 != CmdRiskSafe {
		t.Fatalf("应保持 safe,得 %q", risk3)
	}
}

// TestCommandSuggestNotConfigured 验证未配 AI 时返回 ErrAINotConfigured(供上层降级提示)。
func TestCommandSuggestNotConfigured(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	_, err := svc.CommandSuggest(ctx(), CommandSuggestInput{NL: "看磁盘占用"})
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("未配 AI 应返回 ErrAINotConfigured,得 %v", err)
	}
}

// TestCommandSuggestThreeProviders 验证三 provider 下命令建议解析(stub LLM 返回结构化命令)。
func TestCommandSuggestThreeProviders(t *testing.T) {
	const llmJSON = `{"command":"du -ah . | sort -rh | head -10","explanation":"按大小列出当前目录占用最多的 10 项","risk":"safe","reason":"只读查询,无副作用"}`
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			srv := stubLLM(t, provider, llmJSON)
			svc, _, _ := newService(t, srv.Client())
			configureEnabled(t, svc, provider, srv.URL)

			out, err := svc.CommandSuggest(ctx(), CommandSuggestInput{
				NL:      "磁盘占用 top 10",
				Context: CommandContext{OS: "alpine", Shell: "/bin/sh", Container: "app"},
			})
			if err != nil {
				t.Fatalf("CommandSuggest(%s): %v", provider, err)
			}
			if !strings.Contains(out.Command, "du -ah") {
				t.Fatalf("命令解析错误: %q", out.Command)
			}
			if out.Risk != CmdRiskSafe {
				t.Fatalf("risk 应为 safe,得 %q", out.Risk)
			}
			if out.Explanation == "" {
				t.Fatalf("应有解释")
			}
		})
	}
}

// TestCommandSuggestEscalatesDangerFromAI 验证 AI 给 safe 但命令实危险时,出库前升级 danger(护城河)。
func TestCommandSuggestEscalatesDangerFromAI(t *testing.T) {
	// stub LLM 故意把破坏性命令标 safe(模拟 AI 误判 / 越权)。
	const llmJSON = `{"command":"rm -rf /var/log/*","explanation":"清空日志","risk":"safe","reason":"例行清理"}`
	srv := stubLLM(t, ProviderOpenAI, llmJSON)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	out, err := svc.CommandSuggest(ctx(), CommandSuggestInput{NL: "清日志"})
	if err != nil {
		t.Fatalf("CommandSuggest: %v", err)
	}
	if out.Risk != CmdRiskDanger {
		t.Fatalf("AI 标 safe 的 rm -rf 应被确定性升级为 danger,得 %q", out.Risk)
	}
}

// TestCommandSuggestStripsFence 验证容错剥 markdown fence。
func TestCommandSuggestStripsFence(t *testing.T) {
	fenced := "给你命令:\n```json\n{\"command\":\"ls -la\",\"explanation\":\"列目录\",\"risk\":\"safe\",\"reason\":\"只读\"}\n```"
	srv := stubLLM(t, ProviderClaude, fenced)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderClaude, srv.URL)

	out, err := svc.CommandSuggest(ctx(), CommandSuggestInput{NL: "列出文件"})
	if err != nil {
		t.Fatalf("CommandSuggest: %v", err)
	}
	if out.Command != "ls -la" {
		t.Fatalf("剥 fence 后命令错误: %q", out.Command)
	}
}

// TestExplainCommand 验证命令解释:返回 AI 文本解释 + 确定性风险等级。
func TestExplainCommand(t *testing.T) {
	srv := stubLLM(t, ProviderOpenAI, "ps 列出所有进程,--sort=-%mem 按内存降序,head -6 取前 6 行。")
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	out, err := svc.ExplainCommand(ctx(), ExplainCommandInput{Command: "ps aux --sort=-%mem | head -6"})
	if err != nil {
		t.Fatalf("ExplainCommand: %v", err)
	}
	if !strings.Contains(out.Explanation, "内存降序") {
		t.Fatalf("解释解析错误: %q", out.Explanation)
	}
	if out.Risk != CmdRiskSafe {
		t.Fatalf("ps 应为 safe,得 %q", out.Risk)
	}
}

// TestCompleteCommand 验证命令补全:返回以前缀开头的完整命令。
func TestCompleteCommand(t *testing.T) {
	srv := stubLLM(t, ProviderOpenAI, "docker ps -a --format '{{.Names}}'")
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	out, err := svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "docker ps", Context: CommandContext{Shell: "/bin/sh"}})
	if err != nil {
		t.Fatalf("CompleteCommand: %v", err)
	}
	if out.Completion != "docker ps -a --format '{{.Names}}'" {
		t.Fatalf("补全解析错误: %q", out.Completion)
	}
}

// TestCompleteCommandRejectsNonPrefix 验证补全不以前缀开头时丢弃(回退原前缀,绝不替换用户已输入)。
func TestCompleteCommandRejectsNonPrefix(t *testing.T) {
	srv := stubLLM(t, ProviderOpenAI, "rm -rf /") // 与前缀无关的越权输出
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	out, err := svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls -l"})
	if err != nil {
		t.Fatalf("CompleteCommand: %v", err)
	}
	if out.Completion != "ls -l" {
		t.Fatalf("非前缀补全应回退原前缀,得 %q", out.Completion)
	}
}

// TestCompleteCommandNotConfigured 验证未配 AI → ErrAINotConfigured(前端走本地字典)。
func TestCompleteCommandNotConfigured(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	_, err := svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls"})
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("未配 AI 应 ErrAINotConfigured,得 %v", err)
	}
}

// TestExplainCommandRiskFromClassifier 验证解释场景下风险等级由确定性规则给(不依赖 AI 文本)。
func TestExplainCommandRiskFromClassifier(t *testing.T) {
	srv := stubLLM(t, ProviderOpenAI, "这条命令会递归删除目录。")
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	out, err := svc.ExplainCommand(ctx(), ExplainCommandInput{Command: "rm -rf /data"})
	if err != nil {
		t.Fatalf("ExplainCommand: %v", err)
	}
	if out.Risk != CmdRiskDanger {
		t.Fatalf("rm -rf 解释应标 danger,得 %q", out.Risk)
	}
}
