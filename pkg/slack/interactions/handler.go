package interactions

import (
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

// Handler knows how to handle an interaction callback, optionally
// returning a response body to be sent back to Slack.
type Handler interface {
	Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error)
	Identifier() string
}

type handler struct {
	handle     func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error)
	identifier string
}

func (h *handler) Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	return h.handle(callback, logger)
}
func (h *handler) Identifier() string {
	return h.identifier
}

// HandlerFunc returns a Handler for a handling func
func HandlerFunc(identifier string, handle func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error)) Handler {
	return &handler{
		handle:     handle,
		identifier: identifier,
	}
}

// PartialHandler is a Handler that exposes whether it handled
// a callback, consuming it, or if another Handler should have
// the chance to handle it instead.
type PartialHandler interface {
	Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (handled bool, output []byte, err error)
	Identifier() string
}

type partialHandler struct {
	handle     func(callback *slack.InteractionCallback, logger *logrus.Entry) (handled bool, output []byte, err error)
	identifier string
}

func (h *partialHandler) Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (handled bool, output []byte, err error) {
	return h.handle(callback, logger)
}
func (h *partialHandler) Identifier() string {
	return h.identifier
}

// PartialHandlerFunc returns a PartialHandler for a handling func
func PartialHandlerFunc(identifier string, handle func(callback *slack.InteractionCallback, logger *logrus.Entry) (handled bool, output []byte, err error)) PartialHandler {
	return &partialHandler{
		handle:     handle,
		identifier: identifier,
	}
}

// HandlerFromPartial adapts a partial handler to a singleton
// when there is just one handler for a callback
func HandlerFromPartial(handler PartialHandler) Handler {
	return HandlerFunc(handler.Identifier(), func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		_, output, err = handler.Handle(callback, logger)
		return output, err
	})
}

// PartialFromHandler adapts a handler to a partial
// and always expects that the handler consumes the callback
func PartialFromHandler(handler Handler) PartialHandler {
	return PartialHandlerFunc(handler.Identifier(), func(callback *slack.InteractionCallback, logger *logrus.Entry) (handled bool, output []byte, err error) {
		output, err = handler.Handle(callback, logger)
		return true, output, err
	})
}

// MultiHandler sends the callback to a chain of partial handlers,
// greedily allowing them to consume and respond to it.
func MultiHandler(handlers ...PartialHandler) Handler {
	return HandlerFunc("multi_handler", func(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
		for _, handler := range handlers {
			logger = logger.WithField("handler", handler.Identifier())
			handled, output, err := handler.Handle(callback, logger)
			if handled {
				return output, err
			}
		}
		return nil, nil
	})
}
