package xlate

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func sys(text string) ir.ContentBlock { return ir.ContentBlock{Type: ir.BlockText, Text: text} }
func user(text string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Content: []ir.ContentBlock{sys(text)}}
}
func sysMsg(text string) ir.Message {
	return ir.Message{Role: ir.RoleSystem, Content: []ir.ContentBlock{sys(text)}}
}

func TestCollectSystemReturnsEmptyWhenThereIsNone(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{Messages: []ir.Message{user("hi")}}, "anthropic")
	if got != "" {
		t.Errorf("system = %q, want empty", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestCollectSystemReadsTheTopLevelField(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		System:   []ir.ContentBlock{sys("be terse")},
		Messages: []ir.Message{user("hi")},
	}, "anthropic")
	if got != "be terse" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestCollectSystemPutsTheTopLevelFieldFirst(t *testing.T) {
	got, _ := CollectSystem(&ir.Request{
		System:   []ir.ContentBlock{sys("first")},
		Messages: []ir.Message{sysMsg("second"), user("hi")},
	}, "gemini")
	if got != "first\n\nsecond" {
		t.Errorf("system = %q, want %q", got, "first\n\nsecond")
	}
}

func TestCollectSystemLeadingMessagesProduceNoWarning(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{sysMsg("a"), sysMsg("b"), user("hi")},
	}, "anthropic")
	if got != "a\n\nb" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; leading system messages lose no position", warns)
	}
}

func TestCollectSystemWarnsWhenOneFollowedAUserTurn(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{sysMsg("a"), user("hi"), sysMsg("now be brief")},
	}, "anthropic")
	if got != "a\n\nnow be brief" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
	if warns[0].Field != "messages[].role=system" || warns[0].Target != "anthropic" {
		t.Errorf("warning = %+v", warns[0])
	}
}

func TestCollectSystemWarnsOnceForSeveralMisplacedMessages(t *testing.T) {
	_, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{user("hi"), sysMsg("a"), user("again"), sysMsg("b")},
	}, "gemini")
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
}

func TestCollectSystemWarnsOnNonTextSystemBlocks(t *testing.T) {
	_, warns := CollectSystem(&ir.Request{
		System: []ir.ContentBlock{
			sys("look"),
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		},
	}, "anthropic")
	if len(warns) != 1 || warns[0].Field != "system[].image" {
		t.Fatalf("warnings = %+v", warns)
	}
}

func TestCollectSystemBlocksKeepsBlocksAndTheirCacheControl(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{
		System: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "be terse",
				CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"}},
		},
		Messages: []ir.Message{sysMsg("also be kind"), user("hi")},
	}, "anthropic")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	if len(got) != 2 {
		t.Fatalf("blocks = %+v, want two", got)
	}
	if got[0].CacheControl == nil || got[0].CacheControl.TTL != "1h" {
		t.Errorf("block 0 = %+v; the marker must survive collection", got[0])
	}
	if got[1].Text != "also be kind" {
		t.Errorf("block 1 = %+v", got[1])
	}
}

func TestCollectSystemBlocksWarnsOnMisplacementAndNonText(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{
		System: []ir.ContentBlock{
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		},
		Messages: []ir.Message{user("hi"), sysMsg("now be brief")},
	}, "anthropic")
	if len(got) != 1 || got[0].Text != "now be brief" {
		t.Fatalf("blocks = %+v", got)
	}
	if len(warns) != 2 {
		t.Fatalf("warnings = %+v, want one for the image and one for the position", warns)
	}
}

func TestCollectSystemBlocksReturnsNilWhenThereIsNone(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{Messages: []ir.Message{user("hi")}}, "anthropic")
	if len(got) != 0 || len(warns) != 0 {
		t.Fatalf("blocks = %+v, warnings = %+v", got, warns)
	}
}

func TestNonSystemMessagesDropsSystemTurns(t *testing.T) {
	got := NonSystemMessages([]ir.Message{sysMsg("a"), user("hi"), sysMsg("b")})
	if len(got) != 1 || got[0].Role != ir.RoleUser {
		t.Fatalf("messages = %+v", got)
	}
}

func TestNonSystemMessagesReturnsNilForAllSystem(t *testing.T) {
	if got := NonSystemMessages([]ir.Message{sysMsg("a")}); len(got) != 0 {
		t.Fatalf("messages = %+v", got)
	}
}
