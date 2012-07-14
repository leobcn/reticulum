package node

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// what we know about a single node
// (ourself or another)
type NodeData struct {
	Nickname   string `json:"nickname"`
	UUID       string `json:"uuid"`
	BaseUrl    string `json:"base_url"`
	Location   string `json:"location"`
	Writeable  bool `json:"bool"`
	LastSeen   time.Time `json:"last_seen"`
	LastFailed time.Time `json:"last_failed"`
}

var REPLICAS = 16

func (n NodeData) String() string {
	return "Node - nickname: " + n.Nickname + " UUID: " + n.UUID
}

func (n NodeData) HashKeys() []string {
	keys := make([]string, REPLICAS)
	for i := range keys {
		h := sha1.New()
		io.WriteString(h, fmt.Sprintf("%s%d", n.UUID, i))
		keys[i] = string(fmt.Sprintf("%x", h.Sum(nil)))
	}
	return keys
}

func (n NodeData) IsCurrent() bool {
	return n.LastSeen.Unix() > n.LastFailed.Unix()
}

func (n NodeData) retrieveUrl(hash string, size string, extension string) string {
	return "http://" + n.BaseUrl + "/retrieve/" + hash + "/" + size + "/" + extension + "/"
}

func (n NodeData) stashUrl() string {
	return "http://" + n.BaseUrl + "/stash/"
}

func (n *NodeData) RetrieveImage(hash string, size string, extension string) ([]byte, error) {
	resp, err := http.Get(n.retrieveUrl(hash, size, extension))
	if err != nil {
		n.LastFailed = time.Now()
		return nil, err
	} // otherwise, we go the image
	n.LastSeen = time.Now()
	if resp.Status != "200 OK" {
		return nil, errors.New("404, probably")
	}
	b, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	return b, nil
}

func postFile(filename string, target_url string) (*http.Response, error) { 
  body_buf := bytes.NewBufferString("")
  body_writer := multipart.NewWriter(body_buf)
  file_writer, err := body_writer.CreateFormFile("image", filename)
  if err != nil {
    panic(err.Error())
  }
	fh, err := os.Open(filename)
  if err != nil {
    panic(err.Error())
  }
  io.Copy(file_writer, fh)
  content_type := body_writer.FormDataContentType()
  body_writer.Close()
  return http.Post(target_url, content_type, body_buf)
}

func (n *NodeData) Stash(filename string) bool {
	_, err := postFile(filename, n.stashUrl())
	if err != nil {
		// this node failed us, so take them out of
		// the write ring until we hear otherwise from them
		// TODO: look more closely at the response to 
		//       possibly act differently in specific cases
		//       ie, allow them to specify a temporary failure
		n.LastFailed = time.Now()
		n.Writeable = false
	} else {
		n.LastSeen = time.Now()
	}
	return err == nil
}
