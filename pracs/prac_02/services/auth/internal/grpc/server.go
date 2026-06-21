package authgrpc

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "prac_02/gen/auth/v1"
	"prac_02/services/auth/internal/service"
)

const requestIDMetadataKey = "x-request-id"

type Server struct {
	authv1.UnimplementedAuthServiceServer
	auth *service.AuthService
}

func NewServer(auth *service.AuthService) *Server {
	return &Server{auth: auth}
}

func (s *Server) Verify(ctx context.Context, req *authv1.VerifyRequest) (*authv1.VerifyResponse, error) {
	requestID := requestIDFromContext(ctx)
	log.Printf("service=auth transport=grpc method=Verify request_id=%s token_present=%t", requestID, req.GetToken() != "")

	subject, err := s.auth.VerifyToken(req.GetToken())
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		log.Printf("service=auth transport=grpc method=Verify request_id=%s error=%v", requestID, err)
		return nil, status.Error(codes.Internal, "internal error")
	}

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
