// Package flow connects Feishu record changes and polling to receipt recognition.
// Deprecated: shared processing lives in app/recognition; this facade preserves callers.
package flow

import (
	"context"
	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/events"
	"log/slog"
)

type Config = recognition.Config
type Result = recognition.Result
type ResultHandler = recognition.ResultHandler
type AttachmentReader = recognition.AttachmentReader
type Processor struct {
	*recognition.Processor
	sink *events.AttachmentSink
}

func New(config Config, reader AttachmentReader, recognizer invoice.Recognizer, handler ResultHandler, logger *slog.Logger) (*Processor, error) {
	processor, err := recognition.New(config, reader, recognizer, handler, logger)
	if err != nil {
		return nil, err
	}
	return &Processor{Processor: processor, sink: events.NewAttachmentSink(config, processor)}, nil
}
func (p *Processor) Sink(ctx context.Context, event events.Event) error {
	return p.sink.Sink(ctx, event)
}
