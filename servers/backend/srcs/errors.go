package main

import "errors"

var (
	ErrCategoryNotFound = errors.New("category not found")
	ErrOptionNotFound   = errors.New("option not found")
	ErrAlreadyVoted     = errors.New("already voted in this category")
)
