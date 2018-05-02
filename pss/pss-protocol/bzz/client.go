package bzz

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/ethereum/go-ethereum/log"
)

type Client struct {
	url      string
	resource string
	client   *http.Client
	ready    bool
}

func NewClient(bzzapi string, resource string) *Client {
	return &Client{
		client:   http.DefaultClient,
		resource: resource,
		url:      bzzapi,
	}
}

func (b *Client) createResource(data []byte) error {
	_, err := b.client.Post(
		fmt.Sprintf("%s/bzz-resource:/%s/raw/2", b.url, b.resource),
		"contenxt-type: application/octet-stream",
		bytes.NewBuffer(data),
	)
	if err == nil {
		log.Error("setting ready")
		b.ready = true
	}
	return err
}

func (b *Client) UpdateResource(data []byte) error {
	if !b.ready {
		return b.createResource(data)
	}
	log.Error("is ready")
	_, err := b.client.Post(
		fmt.Sprintf("%s/bzz-resource:/%s/raw", b.url, b.resource),
		"content-type: application/octet-stream",
		bytes.NewBuffer(data),
	)
	return err
}
