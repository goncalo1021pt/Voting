# Vision & Objective

## What this app is

A voting platform built around **awards-show style events**. A host creates an event (e.g. *Game Awards 2026*), defines the categories that will be voted on (Game of the Year, Best RPG, Best 2D Game, …), populates each category with the options people can vote for, and then opens it up to participants.

Think: The Game Awards, The Oscars, end-of-year team awards at a company, friend-group "best-of" polls — anywhere you want a structured, multi-category vote with a clear host and a clear audience.

## Who it's for

- **Hosts** — people running an awards event. They own the event, define what gets voted on, and decide who can participate.
- **Participants** — registered users who join an event and cast their votes.

## Core concepts

- **Event** — the top-level container (e.g. *Game Awards 2026*). Has one host. The platform supports many concurrent events.
- **Category** — a sub-vote inside an event (e.g. *Game of the Year*). Only the host can create categories.
- **Option** — a candidate within a category (e.g. *Baldur's Gate 3*). Only the host can create options.
- **Vote** — a participant's choice of one option in one category.

## How an event works

1. **Creation** — a registered user creates an event and becomes its host. They configure visibility and results behavior.
2. **Setup** — the host adds categories and the options inside each category.
3. **Joining** — participants join the event. Joining is required before voting.
4. **Voting** — each participant casts at most **one vote per category**. Their vote is final for v1.
5. **Conclusion** — the host closes the event. Results are then published.

## Event visibility

Two modes, chosen by the host at creation time:

- **Public** — any registered user can find and join the event freely.
- **Invite-only** — users can only join through an invitation issued by the host. Without an invite, the event is not joinable.

In both modes, **joining is mandatory** before a user can vote. There is no anonymous or drive-by voting.

## Results visibility

Configurable per event, with two modes:

- **After conclusion** *(default)* — results stay hidden until the host closes the event. This protects the event from bandwagon voting and matches the awards-show feel.
- **Live** — running tallies are visible to participants while voting is still open. Useful for casual or transparent polls.

## Voting rules (v1)

- One vote per user per category.
- Votes are final once cast (no changing your mind in v1).
- A user must be a member of the event to vote.

## Future direction (v2 and beyond)

These are explicitly **out of scope for v1** but inform how the schema and code are shaped:

- Richer voting modes — e.g. one vote per day per category, ranked-choice, weighted votes.
- Letting participants change a vote before the event closes.
- Member-suggested options (host still approves).
- Scheduled open/close times for events.
- Notifications when an event you joined closes or publishes results.
