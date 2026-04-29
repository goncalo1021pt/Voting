package main

import "errors"

var (
	ErrCategoryNotFound = errors.New("category not found")
	ErrOptionNotFound   = errors.New("option not found")
	ErrAlreadyVoted     = errors.New("already voted in this category")
	ErrEventNotFound    = errors.New("event not found")
	ErrEventClosed      = errors.New("event is closed")
	ErrEventNotPublic   = errors.New("event is not public")
	ErrNotMember        = errors.New("user is not a member of this event")
	ErrNotHost          = errors.New("user is not the host of this event")
)
