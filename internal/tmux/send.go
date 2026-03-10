package tmux

// SendKeys sends the given string to the named session followed by Enter.
func SendKeys(sessionName, keys string) error {
	return run("send-keys", "-t", sessionName, keys, "Enter")
}

// SendKeysRaw sends the given string to the named session without appending
// Enter, allowing callers to send arbitrary key sequences.
func SendKeysRaw(sessionName, keys string) error {
	return run("send-keys", "-t", sessionName, keys)
}
