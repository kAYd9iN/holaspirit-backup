package credentials

// Manager abstracts secret retrieval for testability.
type Manager interface {
	GetToken() (string, error)
}
