package bzz

import (
	"bytes"
	"fmt"
	"net/http"
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
		fmt.Sprintf("http://%s/bzz-resource:/%s/raw/2"),
		"contenxt-type: application/octet-stream",
		bytes.NewBuffer(data),
	)
	if err != nil {
		b.ready = true
	}
	return err
}

func (b *Client) UpdateResource(data []byte) error {
	if !b.ready {
		return b.createResource(data)
	}
	_, err := b.client.Post(
		fmt.Sprintf("http://%s/bzz-resource:/%s/raw"),
		"contenxt-type: application/octet-stream",
		bytes.NewBuffer(data),
	)
	return err
}
