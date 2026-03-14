package extractor

import (
	"fmt"
	"github.com/jhillyerd/enmime"
	"io"
)

type EmailExtractor struct{}

func (e *EmailExtractor) ExtractMbox(r io.Reader) (string, error) {
    // Basic MBOX/EML parsing
    env, err := enmime.ReadEnvelope(r)
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("Subject: %s\nFrom: %s\n\n%s", env.GetHeader("Subject"), env.GetHeader("From"), env.Text), nil
}
