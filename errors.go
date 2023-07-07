package main

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNotRSSFeed    = errors.New("not a valid feed")
	ErrAlreadyExists = errors.New("already exists")
	ErrEmptyFeed     = errors.New("empty feed")
)

type ErrHTTP struct {
	StatusCode int
}

func (e *ErrHTTP) Error() string {
	return strings.ToLower(http.StatusText(e.StatusCode))
}
