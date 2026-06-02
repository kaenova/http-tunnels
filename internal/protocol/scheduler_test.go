package protocol

import "testing"

func TestFrameSchedulerRoundRobin(t *testing.T) {
	s := NewFrameScheduler(8)
	defer s.Close()

	must := func(err error) {
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	must(s.Enqueue(&Frame{Type: FrameType_RESPONSE_BODY, RequestId: "a", Chunk: []byte("a1")}))
	must(s.Enqueue(&Frame{Type: FrameType_RESPONSE_BODY, RequestId: "a", Chunk: []byte("a2")}))
	must(s.Enqueue(&Frame{Type: FrameType_RESPONSE_BODY, RequestId: "b", Chunk: []byte("b1")}))
	must(s.Enqueue(&Frame{Type: FrameType_RESPONSE_BODY, RequestId: "b", Chunk: []byte("b2")}))

	got := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		frame, err := s.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		got = append(got, frame.GetRequestId()+string(frame.GetChunk()))
	}

	want := []string{"aa1", "bb1", "aa2", "bb2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v want %v", i, got, want)
		}
	}
}

func TestFrameSchedulerPrioritizesControlFrames(t *testing.T) {
	s := NewFrameScheduler(8)
	defer s.Close()

	if err := s.Enqueue(&Frame{Type: FrameType_RESPONSE_BODY, RequestId: "req-1", Chunk: []byte("body")}); err != nil {
		t.Fatalf("enqueue request: %v", err)
	}
	if err := s.EnqueueControl(&Frame{Type: FrameType_PONG}); err != nil {
		t.Fatalf("enqueue control: %v", err)
	}

	frame, err := s.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if frame.GetType() != FrameType_PONG {
		t.Fatalf("expected control frame first, got %v", frame.GetType())
	}
}
