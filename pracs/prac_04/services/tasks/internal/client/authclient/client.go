package authclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "prac_04/gen/auth/v1"
	"prac_04/shared/middleware"
)

var ErrUnauthorized = errors.New("unauthorized")
var ErrAuthUnavailable = errors.New("auth unavailable")

type Client struct {
	conn    *grpc.ClientConn
	client  authv1.AuthServiceClient
	timeout time.Duration
	log     *logrus.Entry
}

func New(addr string, log *logrus.Entry) (*Client, error) {
	conn, err := grpc.Dial(
		strings.TrimSpace(addr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial auth grpc: %w", err)
	}

	return &Client{
		conn:    conn,
		client:  authv1.NewAuthServiceClient(conn),
		timeout: 2 * time.Second,
		log:     log.WithField("component", "auth_client"),
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Verify(ctx context.Context, authorization, requestID string) error {
	log := c.log.WithField("request_id", requestID)

	token, err := bearerToken(authorization)
	if err != nil {
		log.Warn("missing or invalid bearer token")
		return ErrUnauthorized
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if requestID != "" {
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(strings.ToLower(middleware.HeaderRequestID), requestID))
	}

	log.Debug("calling grpc auth.Verify")

	resp, err := c.client.Verify(ctx, &authv1.VerifyRequest{Token: token})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			log.WithError(err).Error("verify rpc failed")
			return fmt.Errorf("verify rpc: %w", err)
		}

		switch st.Code() {
		case codes.Unauthenticated:
			log.Warn("auth service returned unauthenticated")
			return ErrUnauthorized
		case codes.DeadlineExceeded, codes.Internal, codes.Unavailable:
			log.WithError(err).Error("auth service unavailable")
			return ErrAuthUnavailable
		default:
			log.WithError(err).Error("verify rpc unexpected error")
			return fmt.Errorf("verify rpc: %w", err)
		}
	}

	if !resp.GetValid() {
		log.Warn("auth service returned invalid token")
		return ErrUnauthorized
	}

	log.Debug("auth verify succeeded")
	return nil
}

func bearerToken(authorization string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrUnauthorized
	}

	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	if token == "" {
		return "", ErrUnauthorized
	}

	return token, nil
}
