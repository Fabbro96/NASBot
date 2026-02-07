package main

import (
	"time"
)

// AddReportEvent adds an event to the daily report log
func (s *RuntimeState) AddReportEvent(level, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	event := ReportEvent{
		Time:    time.Now(),
		Type:    level,
		Message: message,
	}
	s.ReportEvents = append(s.ReportEvents, event)
}
