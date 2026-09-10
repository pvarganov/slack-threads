package app

// withBuilder replaces the production wiring, so a test can exercise the
// startup path without a real Slack client or a claude process.
func withBuilder(b buildFunc) Option {
	return func(a *App) { a.build = b }
}
