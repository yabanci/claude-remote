package telegram

func WithSleeper(sleep Sleeper) Option {
	return func(c *Client) { c.sleep = sleep }
}
