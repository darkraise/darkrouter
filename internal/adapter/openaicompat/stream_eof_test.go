package openaicompat

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func streamErr(body string) error {
	for _, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestAStreamClosedBeforeAnyFinishIsAnError(t *testing.T) {
	// Half a tool argument object, then the connection closes. Ending the
	// sequence cleanly lets a writer render it as a finished completion.
	body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\"," +
		"\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n"
	if err := streamErr(body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestAnEmptyStreamIsAnError(t *testing.T) {
	if err := streamErr(""); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestAFinishedStreamWithoutTheDoneSentinelIsComplete(t *testing.T) {
	// Some compatible upstreams stop after the finish reason and usage chunk.
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"
	if err := streamErr(body); err != nil {
		t.Fatalf("err = %v", err)
	}
}
