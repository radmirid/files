package main

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/radmirid/files/api"
)

const (
	maxDownloadConnections = 10
	maxListConnections     = 100
)

type server struct {
	pb.UnimplementedFileServiceServer
	mu                  sync.Mutex
	files               []string
	downloadConnections int
	listConnections     int
}

func receiveFile(stream pb.FileService_UploadFileServer) (*pb.File, error) {
	var name string
	var data []byte

	for {
		file, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name = file.Name
		data = append(data, file.Data...)
	}

	return &pb.File{Name: name, Data: data}, nil
}

func saveFile(file *pb.File) error {
	f, err := os.Create(file.Name)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(file.Data); err != nil {
		return err
	}
	return nil
}

func (s *server) UploadFile(stream pb.FileService_UploadFileServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := receiveFile(stream)
	if err != nil {
		return err
	}

	if err := saveFile(file); err != nil {
		return err
	}

	s.files = append(s.files, file.Name)

	return nil
}

func (s *server) ListFiles(ctx context.Context, req *pb.ListFilesRequest) (*pb.ListFilesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listConnections >= maxListConnections {
		return nil, status.Errorf(codes.ResourceExhausted, "maxListConnections limit")
	}
	s.listConnections++
	defer func() { s.listConnections-- }()
	return &pb.ListFilesResponse{Files: s.files}, nil
}

func (s *server) DownloadFile(req *pb.DownloadFileRequest, stream pb.FileService_DownloadFileServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.downloadConnections >= maxDownloadConnections {
		return status.Errorf(codes.ResourceExhausted, "maxDownloadConnections limit")
	}
	s.downloadConnections++
	defer func() { s.downloadConnections-- }()
	var filePath string
	for _, f := range s.files {
		if f == req.Name {
			filePath = f
			break
		}
	}
	if filePath == "" {
		return status.Errorf(codes.NotFound, "file not found")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 1024)
	for {
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if err := stream.Send(&pb.DownloadFileResponse{Data: buf[:n]}); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	s := grpc.NewServer()
	pb.RegisterFileServiceServer(s, &server{})

	listen, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Println("Starting server on :8080")
	if err := s.Serve(listen); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
