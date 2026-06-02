package protocol

import (
	"errors"
	"sync"
)

var ErrSchedulerClosed = errors.New("frame scheduler closed")

const DefaultPerRequestFrameQueue = 16

type FrameScheduler struct {
	mu              sync.Mutex
	cond            *sync.Cond
	closed          bool
	perRequestLimit int
	control         []*Frame
	queues          map[string][]*Frame
	order           []string
	nextIndex       int
}

func NewFrameScheduler(perRequestLimit int) *FrameScheduler {
	if perRequestLimit <= 0 {
		perRequestLimit = DefaultPerRequestFrameQueue
	}
	s := &FrameScheduler{
		perRequestLimit: perRequestLimit,
		queues:          make(map[string][]*Frame),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *FrameScheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.control = nil
	s.queues = nil
	s.order = nil
	s.cond.Broadcast()
}

func (s *FrameScheduler) EnqueueControl(frame *Frame) error {
	if frame == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSchedulerClosed
	}
	s.control = append(s.control, frame)
	s.cond.Signal()
	return nil
}

func (s *FrameScheduler) Enqueue(frame *Frame) error {
	if frame == nil {
		return nil
	}
	requestID := frame.GetRequestId()
	if requestID == "" {
		return s.EnqueueControl(frame)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.closed {
			return ErrSchedulerClosed
		}
		queue := s.queues[requestID]
		if len(queue) < s.perRequestLimit {
			if len(queue) == 0 {
				s.order = append(s.order, requestID)
			}
			s.queues[requestID] = append(queue, frame)
			s.cond.Signal()
			return nil
		}
		s.cond.Wait()
	}
}

func (s *FrameScheduler) Next() (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if len(s.control) > 0 {
			frame := s.control[0]
			s.control = s.control[1:]
			return frame, nil
		}
		if frame := s.popNextRequestLocked(); frame != nil {
			s.cond.Broadcast()
			return frame, nil
		}
		if s.closed {
			return nil, ErrSchedulerClosed
		}
		s.cond.Wait()
	}
}

func (s *FrameScheduler) popNextRequestLocked() *Frame {
	if len(s.order) == 0 {
		return nil
	}

	visited := 0
	for len(s.order) > 0 && visited < len(s.order) {
		if s.nextIndex >= len(s.order) {
			s.nextIndex = 0
		}
		requestID := s.order[s.nextIndex]
		queue := s.queues[requestID]
		if len(queue) == 0 {
			delete(s.queues, requestID)
			s.removeOrderIndexLocked(s.nextIndex)
			continue
		}

		frame := queue[0]
		queue = queue[1:]
		if len(queue) == 0 {
			delete(s.queues, requestID)
			s.removeOrderIndexLocked(s.nextIndex)
		} else {
			s.queues[requestID] = queue
			s.nextIndex = (s.nextIndex + 1) % len(s.order)
		}
		return frame
	}
	return nil
}

func (s *FrameScheduler) removeOrderIndexLocked(idx int) {
	if idx < 0 || idx >= len(s.order) {
		return
	}
	s.order = append(s.order[:idx], s.order[idx+1:]...)
	if len(s.order) == 0 {
		s.nextIndex = 0
		return
	}
	if idx < s.nextIndex {
		s.nextIndex--
	}
	if s.nextIndex >= len(s.order) {
		s.nextIndex = 0
	}
}
