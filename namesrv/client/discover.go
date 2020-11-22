package client

import "github.com/klintcheng/fim/wire"

type NamingClient struct {
}

type Options struct {
}

func NewNamingClient(opts Options) (*NamingClient, error) {

	return nil, nil
}

func (n *NamingClient) GetServers() ([]wire.LgServer, error) {

	return nil, nil
}
