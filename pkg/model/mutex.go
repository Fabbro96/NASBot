//go:build !deadlock

package model

import "sync"

// Mutex is an alias for sync.Mutex in production builds.
type Mutex = sync.Mutex

// RWMutex is an alias for sync.RWMutex in production builds.
type RWMutex = sync.RWMutex
