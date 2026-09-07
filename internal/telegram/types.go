package telegram

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64     `json:"message_id"`
	Chat      Chat      `json:"chat"`
	From      *User     `json:"from"`
	Text      string    `json:"text"`
	Document  *Document `json:"document"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}
