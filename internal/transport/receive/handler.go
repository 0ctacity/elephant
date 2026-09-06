// Package receive is the versioned, bounded machine-to-machine JSON boundary.
package receive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"elephant/internal/app"
)

const MaxMessageBytes = 1 << 20

func Handle(ctx context.Context, s *app.Service, in io.Reader, out io.Writer) error {
	limited := io.LimitReader(in, MaxMessageBytes+1)
	data, err := io.ReadAll(limited)
	var m app.Message
	if err == nil && len(data) > MaxMessageBytes {
		err = fmt.Errorf("message exceeds 1 MiB")
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&m)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("expected one JSON message")
			}
		}
	}
	response := app.Response{ProtocolVersion: 1, MessageID: m.MessageID}
	if err == nil {
		response, err = s.Receive(ctx, m)
	}
	if err != nil {
		response.Error = err.Error()
		response.ElephantID, _ = s.Identity(ctx)
	}
	return json.NewEncoder(out).Encode(response)
}
