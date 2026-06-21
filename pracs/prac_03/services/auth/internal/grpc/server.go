package authgrpc

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "prac_03/gen/auth/v1"
	"prac_03/services/auth/internal/service"
)

const requestIDMetadataKey = "x-request-id"

type Server struct {
	authv1.UnimplementedAuthServiceServer
	auth *service.AuthService
	log  *logrus.Entry
}

func NewServer(auth *service.AuthService, log *logrus.Entry) *Server {
	return &Server{auth: auth, log: log.WithField("component", "grpc")}
}

func (s *Server) Verify(ctx context.Context, req *authv1.VerifyRequest) (*authv1.VerifyResponse, error) {
	requestID := requestIDFromContext(ctx)
	log := s.log.WithField("request_id", requestID)

	log.WithField("has_token", req.GetToken() != "").Debug("verify called")

	subject, err := s.auth.VerifyToken(req.GetToken())
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			log.Warn("verify failed: invalid token")
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		log.WithError(err).Error("verify internal error")
		return nil, status.Error(codes.Internal, "internal error")
	}

	log.WithField("subject", subject).Debug("verify succeeded")

	return &authv1.VerifyResponse{
		Valid:   true,
		Subject: subject,
	}, nil
}

func requestIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	values := md.Get(requestIDMetadataKey)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
