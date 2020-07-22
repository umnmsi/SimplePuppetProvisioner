package githubwebhook

type GitHubWebhookResult struct {
	Event   string
	Commits []string
	UUID    string
}
