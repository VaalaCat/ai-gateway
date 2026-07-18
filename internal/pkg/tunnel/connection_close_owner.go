package tunnel

import "sync"

type ConnectionCloseOwner struct {
	closeOnce sync.Once
	close     func() error
	err       error
}

func NewConnectionCloseOwner(close func() error) *ConnectionCloseOwner {
	return &ConnectionCloseOwner{close: close}
}

func (o *ConnectionCloseOwner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		if o.close != nil {
			o.err = o.close()
		}
	})
	return o.err
}
