/*
Copyright 2026 KubeLease Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sourcewebhook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ListenAndServe starts the webhook HTTP server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.Config.Defaults()
	if s.TokenProvider == nil {
		return fmt.Errorf("token provider is required")
	}
	if s.Client == nil {
		return fmt.Errorf("client is required")
	}
	if s.Config.DefaultPolicy == "" {
		return fmt.Errorf("default policy is required")
	}

	srv := &http.Server{
		Addr:              s.Config.BindAddress,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       s.Config.ReadTimeout,
		WriteTimeout:      s.Config.WriteTimeout,
		IdleTimeout:       s.Config.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	errCh := make(chan error, 1)
	go func() {
		s.Log.Info("webhook source listening", "addr", s.Config.BindAddress)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}
