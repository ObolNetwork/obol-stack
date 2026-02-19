//go:build !darwin || !cgo

package enclave

// stubKey satisfies the Key interface on unsupported platforms.
type stubKey struct{ tag string }

func (s *stubKey) PublicKeyBytes() []byte                   { return nil }
func (s *stubKey) Sign(_ []byte) ([]byte, error)            { return nil, ErrNotSupported }
func (s *stubKey) ECDH(_ []byte) ([]byte, error)            { return nil, ErrNotSupported }
func (s *stubKey) Decrypt(_ []byte) ([]byte, error)         { return nil, ErrNotSupported }
func (s *stubKey) Tag() string                              { return s.tag }
func (s *stubKey) Persistent() bool                         { return false }
func (s *stubKey) Delete() error                            { return ErrNotSupported }

func newKey(_ string) (Key, error)                          { return nil, ErrNotSupported }
func loadKey(_ string) (Key, error)                         { return nil, ErrNotSupported }
func deleteKey(_ string) error                              { return ErrNotSupported }
func checkSIP() error                                       { return ErrNotSupported }
func decrypt(_ string, _ []byte) ([]byte, error)            { return nil, ErrNotSupported }
