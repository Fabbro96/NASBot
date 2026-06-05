//go:build deadlock

package model

import "github.com/sasha-s/go-deadlock"

// Mutex is an alias for deadlock.Mutex in debug builds.
type Mutex = deadlock.Mutex

// RWMutex is an alias for deadlock.RWMutex in debug builds.
type RWMutex = deadlock.RWMutex
