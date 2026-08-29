package main

import "errors"

var (
	ErrCategoryNotFound   = errors.New("category not found")
	ErrOptionNotFound     = errors.New("option not found")
	ErrAlreadyVoted       = errors.New("already voted in this category")
	ErrEventNotFound      = errors.New("event not found")
	ErrEventClosed        = errors.New("event is closed")
	ErrEventNotPublic     = errors.New("event is not public")
	ErrNotMember          = errors.New("user is not a member of this event")
	ErrNotHost            = errors.New("user is not the host of this event")
	ErrSessionInvalid     = errors.New("session invalid or expired")
	ErrInvitationNotFound = errors.New("invitation not found")
	ErrInvitationRedeemed = errors.New("invitation already redeemed")
	ErrInvitationExpired  = errors.New("invitation expired")
	ErrAlreadyMember      = errors.New("user is already a member of this event")
	ErrMemberNotFound     = errors.New("member not found")
	ErrCannotRemoveHost   = errors.New("the host cannot be removed from their own event")
	ErrBallotIncomplete   = errors.New("ballot does not cover every category")
	ErrFullBallotRequired = errors.New("this event requires a complete ballot")
	ErrDuplicateCategory  = errors.New("ballot has more than one vote for a category")
	ErrBallotEmpty        = errors.New("ballot contains no votes")

	// Editing an event may not silently discard ballots: options and
	// categories cascade to votes, so removing one that has been voted on
	// would rewrite a tally rather than correct a mistake.
	ErrCategoryHasVotes = errors.New("category has votes and cannot be removed")
	ErrOptionHasVotes   = errors.New("option has votes and cannot be removed")

	// ErrDuplicateEditRef is the same existing category or option listed twice
	// in one edit. The end state it asks for is ambiguous, so it is refused
	// rather than resolved by whichever entry happens to be applied last.
	ErrDuplicateEditRef = errors.New("the same category or option appears twice in the edit")

	// ErrUsernameTaken is a rename losing to the UNIQUE(username) constraint.
	ErrUsernameTaken = errors.New("username already taken")
	ErrUserNotFound  = errors.New("user not found")
)
