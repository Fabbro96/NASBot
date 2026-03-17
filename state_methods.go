package main

// AddReportEvent adds an event to the daily report log
func (s *RuntimeState) AddReportEvent(level, message string) {
	// Keep a single event ingestion path to preserve trimming and lock semantics.
	s.AddEvent(level, message)
}
