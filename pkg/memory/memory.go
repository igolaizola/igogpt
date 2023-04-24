package memory

type Message struct {
	Role    string
	Content string
}

type Memory interface {
	Add(Message) error
	Sum() ([]Message, error)
}
