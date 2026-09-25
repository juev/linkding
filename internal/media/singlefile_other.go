//go:build !linux && !darwin && !freebsd

package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

func runSingleFileProcess(ctx context.Context, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("single-file timeout or cancellation: %w", ctx.Err())
		}
		return fmt.Errorf("single-file: %w: %s", err, stderr.String())
	}
	return nil
}
