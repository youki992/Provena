package handler

import (
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/mcp/builtin"
)

func TestPiHarnessDoesNotRequireSkillOrRole(t *testing.T) {
	if piNeedsHarness("你是谁") {
		t.Fatal("ordinary conversation should not enter the Harness")
	}
	if !piNeedsHarness("帮我运行 waybackurls") {
		t.Fatal("an explicit local tool request should enter the Harness")
	}
	request := "CTF 挑战 https://target.test，测试 SQL 注入并获取 flag"
	if got := piTaskKind(request); got != piTaskKindCTF {
		t.Fatalf("task kind = %q, want ctf", got)
	}
	gate := newPiExecutionGate(request, `C:\skills`)
	if gate == nil {
		t.Fatal("legacy gate helper should remain constructible for compatibility")
	}
	if allowed, reason := gate.allowBridgeCall(builtin.ToolCreateAsset, nil); allowed || !strings.Contains(reason, "CTF") {
		t.Fatalf("CTF must block persistent asset writes: allowed=%v reason=%q", allowed, reason)
	}
	if allowed, reason := gate.allowBridgeCall(builtin.ToolRecordVulnerability, nil); allowed || !strings.Contains(reason, "CTF") {
		t.Fatalf("CTF must block persistent vulnerability writes: allowed=%v reason=%q", allowed, reason)
	}
}

func TestPiTaskKindDoesNotClassifyBareURLAsPentest(t *testing.T) {
	if got := piTaskKind("请访问 https://target.test 并告诉我页面标题"); got != "" {
		t.Fatalf("non-security URL request classified as %q", got)
	}
	if got := piTaskKind("对 https://target.test 做授权测试"); got != piTaskKindPentest {
		t.Fatalf("authorized test classified as %q", got)
	}
}

func TestPiExecutionGateRecognizesReconAndRejectsExploitCommand(t *testing.T) {
	if !piIsReconObservation("bash", map[string]interface{}{"command": "curl -I https://target.test"}) {
		t.Fatal("curl probe should count as reconnaissance")
	}
	if piIsReconObservation("bash", map[string]interface{}{"command": "curl -s 'https://target.test/?id=1 union select 1,2'"}) {
		t.Fatal("exploit-shaped curl should not satisfy reconnaissance")
	}
	if !piIsValidationBridgeTool("dddd", map[string]interface{}{"full_poc": true}) {
		t.Fatal("dddd full_poc should require reconnaissance")
	}
}

func TestPiTestProfileRoutesCapturedPacketsToAPI(t *testing.T) {
	profile := piTestProfileForRequest("测试目标并分析已选择的请求包\n" + piPacketCaptureRoutingMarker)
	if profile.ID != piProfileAPI || profile.Label != "API 测试" {
		t.Fatalf("captured packet profile = %#v, want API", profile)
	}
	if profile.ArchiveReadme != "API Pentesting/README.md" {
		t.Fatalf("captured packet archive route = %q", profile.ArchiveReadme)
	}
}

func TestPiTestProfileRoutesSpecializedTasks(t *testing.T) {
	cases := []struct {
		request, wantID, wantReadme string
	}{
		{"授权测试 AWS S3 和 IAM", piProfileCloud, "Cloud Pentesting/README.md"},
		{"审计 Android APK", piProfileMobile, "Mobile Pentesting/Readme.md"},
		{"检查 Kubernetes 集群", piProfileContainer, "Container & Kubernetes  Assessment/README.md"},
		{"对 Solidity 智能合约做安全测试", piProfileBlockchain, "BlockChain Pentesting/README.md"},
		{"做源码代码审计", piProfileCode, "Secure Code Review/README.md"},
	}
	for _, tc := range cases {
		profile := piTestProfileForRequest(tc.request)
		if profile.ID != tc.wantID || profile.ArchiveReadme != tc.wantReadme {
			t.Errorf("profile for %q = %#v, want %s/%s", tc.request, profile, tc.wantID, tc.wantReadme)
		}
	}
}
