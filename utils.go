package ethereum

import (
	"time"
)

func Retry(
	fn func() error,
	maxAttempts int, sleepTime time.Duration) {

	for i := 0; ; i++ {
		if err := fn(); err == nil {
			return
		}

		if i == maxAttempts-1 {
			return
		}

		time.Sleep(sleepTime)
	}
}
