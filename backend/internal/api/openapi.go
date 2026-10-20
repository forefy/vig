package api

import (
	"io"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// WriteOpenAPI renders the spec without starting a server. Used by `vig
// openapi` and by the frontend client generation. The dev-only login route
// is never part of the spec.
func WriteOpenAPI(w io.Writer, adapters map[string]core.Adapter) error {
	d := Contract(adapters)
	api := humachi.New(chi.NewRouter(), humaConfig())
	d.api = api
	Register(api, d)
	b, err := api.OpenAPI().MarshalJSON()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
