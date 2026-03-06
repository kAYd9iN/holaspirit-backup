package credentials

// Mock is a test double for Manager.
type Mock struct {
	Token string
	Err   error
}

func (m *Mock) GetToken() (string, error) {
	return m.Token, m.Err
}
