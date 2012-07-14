package views

import (
	"../models"
	"../resize_worker"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Page struct {
	Title      string
	RequireKey bool
}

type ImageData struct {
	Hash      string `json:"hash"`
	Length    int `json:"length"`
	Extension string `json:"extension"`
	FullUrl string `json:"full_url"`
	Satisfied bool `json:"satisfied"`
	Nodes []string `json:"nodes"`
}

func renderTemplate(w http.ResponseWriter, tmpl string, p *Page) {
	t, _ := template.ParseFiles("templates/" + tmpl + ".html")
	t.Execute(w, p)
}

func hashToPath(h []byte) string {
	buffer := bytes.NewBufferString("")
	for i := range h {
		fmt.Fprint(buffer, fmt.Sprintf("%02x/", h[i]))
	}
	return buffer.String()
}

func hashStringToPath(h string) string {
	var parts []string
	for i := range h {
		if (i % 2) != 0 {
			parts = append(parts, h[i-1:i+1])
		}
	}
	return strings.Join(parts, "/")
}

var jpeg_options = jpeg.Options{Quality: 90}

func retrieveImage(cluster *models.Cluster, ahash string, size string, extension string) ([]byte, error){
	// we don't have the full-size, so check the cluster
	nodes_to_check := cluster.ReadOrder(ahash)
	// this is where we go down the list and ask the other
	// nodes for the image
	// TODO: parallelize this
	for _, n := range nodes_to_check {
		if n.UUID == cluster.Myself.UUID {
			// checking ourself would be silly
			continue
		}
		img, err := n.RetrieveImage(ahash, size, extension)
		if err == nil {
			// got it, return it
			return img, nil
		}
		// that node didn't have it so we keep going
	}
	return nil, errors.New("not found in the cluster")
}

func ServeImageHandler(w http.ResponseWriter, r *http.Request, cluster *models.Cluster,
	siteconfig models.SiteConfig, channels models.SharedChannels) {
	parts := strings.Split(r.URL.String(), "/")
	if (len(parts) < 5) || (parts[1] != "image") {
		http.Error(w, "bad request", 404)
		return
	}
	ahash := parts[2]
	size := parts[3]
	filename := parts[4]
	if filename == "" {
		filename = "image.jpg"
	}
	extension := filepath.Ext(filename)
	if len(ahash) != 40 {
		http.Error(w, "bad hash", 404)
		return
	}

	baseDir := siteconfig.UploadDirectory + hashStringToPath(ahash)
	path := baseDir + "/full" + extension
	sizedPath := baseDir + "/" + size + extension

	contents, err := ioutil.ReadFile(sizedPath)
	if err == nil {
		// we've got it, so serve it directly
		w.Header().Set("Content-Type", extmimes[extension[1:]])
		w.Write(contents)
		return
	}

	_, err = ioutil.ReadFile(path)
	if err != nil {
		// we don't have the full-size on this node either
		// need to check the rest of the cluster
		img_data, err := retrieveImage(cluster, ahash, size, extension[1:])
		if err != nil {
			// for now we just have to 404
			http.Error(w, "not found", 404)
		} else {
			w.Header().Set("Content-Type", extmimes[extension[1:]])
			w.Write(img_data)
		}
		return
	}

	// we do have the full-size, but not the scaled one
	// so resize it, cache it, and serve it.

	c := make(chan resize_worker.ResizeResponse)
	channels.ResizeQueue <- resize_worker.ResizeRequest{path, extension, size, c}
	result := <-c
	outputImage := *result.OutputImage
	// TODO handle resize errors

	wFile, err := os.OpenFile(sizedPath, os.O_CREATE|os.O_RDWR, 0644)
	defer wFile.Close()
	if err != nil {
		// what do we do if we can't write?
		// we still have the resized image, so we can serve the response
		// we just can't cache it. 
	}
	w.Header().Set("Content-Type", extmimes[extension[1:]])
	if extension == ".jpg" {
		jpeg.Encode(wFile, outputImage, &jpeg_options)
		jpeg.Encode(w, outputImage, &jpeg_options)
		return
	}
	if extension == ".gif" {
		// image/gif doesn't include an Encode()
		// so we'll use png for now. 
		// :(
		png.Encode(wFile, outputImage)
		png.Encode(w, outputImage)
		return
	}
	if extension == ".png" {
		png.Encode(wFile, outputImage)
		png.Encode(w, outputImage)
		return
	}

}

var mimeexts = map[string]string{
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/png":  "png",
}

var extmimes = map[string]string{
	"jpg": "image/jpeg",
	"gif": "image/gif",
	"png": "image/png",
}

func AddHandler(w http.ResponseWriter, r *http.Request, cluster *models.Cluster,
	siteconfig models.SiteConfig, channels models.SharedChannels) {
	if r.Method == "POST" {
		if siteconfig.KeyRequired() {
			if !siteconfig.ValidKey(r.FormValue("key")) {
				http.Error(w, "invalid upload key", 403)
				return
			}
		}
		i, fh, _ := r.FormFile("image")
		h := sha1.New()
		d, _ := ioutil.ReadAll(i)
		io.WriteString(h, string(d))
		ahash := fmt.Sprintf("%x", h.Sum(nil))
		path := siteconfig.UploadDirectory + hashToPath(h.Sum(nil))
		os.MkdirAll(path, 0755)
		mimetype := fh.Header["Content-Type"][0]
		ext := mimeexts[mimetype]
		fullpath := path + "full." + ext
		f, _ := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, 0644)
		defer f.Close()
		n, _ := f.Write(d)

		// now stash it to other nodes in the cluster too
		nodes := cluster.Stash(ahash, fullpath, siteconfig.Replication)

		id := ImageData{
			Hash:      ahash,
			Length:    n,
			Extension: ext,
		  FullUrl: "/image/" + ahash + "/full/image." + ext,
  	  Satisfied: len(nodes) >= siteconfig.Replication,
  		Nodes: nodes,
		}
		b, err := json.Marshal(id)
		if err != nil {
			fmt.Println("error:", err)
		}
		w.Write(b)
	} else {
		p := Page{
			Title:      "upload image",
			RequireKey: siteconfig.KeyRequired(),
		}
		renderTemplate(w, "add", &p)
	}
}

func StashHandler(w http.ResponseWriter, r *http.Request, cluster *models.Cluster,
	siteconfig models.SiteConfig, channels models.SharedChannels) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 400)
		return
	}
	i, fh, _ := r.FormFile("image")
	h := sha1.New()
	d, _ := ioutil.ReadAll(i)
	io.WriteString(h, string(d))
	path := siteconfig.UploadDirectory + hashToPath(h.Sum(nil))
	os.MkdirAll(path, 0755)
	ext := filepath.Ext(fh.Filename)

	fullpath := path + "full" + ext
	// TODO: if target file already exists, no need to overwrite
	f, _ := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, 0644)
	defer f.Close()
	f.Write(d)
}

func RetrieveHandler(w http.ResponseWriter, r *http.Request, cluster *models.Cluster,
	siteconfig models.SiteConfig, channels models.SharedChannels) {

	// request will look like /retrieve/$hash/$size/$ext/
	parts := strings.Split(r.URL.String(), "/")
	if (len(parts) != 6) || (parts[1] != "retrieve") {
		http.Error(w, "bad request", 404)
		return
	}
	ahash := parts[2]
	size := parts[3]
	extension := parts[4]

	if len(ahash) != 40 {
		http.Error(w, "bad hash", 404)
		return
	}
	baseDir := siteconfig.UploadDirectory + hashStringToPath(ahash)
	path := baseDir + "/full" + "." + extension
	sizedPath := baseDir + "/" + size + "." + extension

	contents, err := ioutil.ReadFile(sizedPath)
	if err == nil {
		// we've got it, so serve it directly
		w.Header().Set("Content-Type", extmimes[extension])
		w.Write(contents)
		return
	}
	_, err = ioutil.ReadFile(path)
	if err != nil {
		// we don't have the full-size on this node either
		http.Error(w, "not found", 404)
		return
	}
	// we do have the full-size, but not the scaled one
	// so resize it, cache it, and serve it.

	c := make(chan resize_worker.ResizeResponse)
	channels.ResizeQueue <- resize_worker.ResizeRequest{path, "." + extension, size, c}
	result := <-c
	outputImage := *result.OutputImage
	// TODO handle resize errors

	wFile, err := os.OpenFile(sizedPath, os.O_CREATE|os.O_RDWR, 0644)
	defer wFile.Close()
	if err != nil {
		// what do we do if we can't write?
		// we still have the resized image, so we can serve the response
		// we just can't cache it. 
	}
	w.Header().Set("Content-Type", extmimes[extension])
	if extension == "jpg" {
		jpeg.Encode(wFile, outputImage, &jpeg_options)
		jpeg.Encode(w, outputImage, &jpeg_options)
		return
	}
	if extension == "gif" {
		// image/gif doesn't include an Encode()
		// so we'll use png for now. 
		// :(
		png.Encode(wFile, outputImage)
		png.Encode(w, outputImage)
		return
	}
	if extension == "png" {
		png.Encode(wFile, outputImage)
		png.Encode(w, outputImage)
		return
	}
}

type AnnounceResponse struct {
	Nickname  string `json:"nickname"`
	UUID      string `json:"uuid"`
	Location  string `json:"location"`
	Writeable bool `json:"writeable"`
	BaseUrl   string `json:"base_url"` 
	Neighbors []models.NodeData `json:"neighbors"`
}

func AnnounceHandler(w http.ResponseWriter, r *http.Request,
	cluster *models.Cluster, siteconfig models.SiteConfig,
	channels models.SharedChannels) {
	if r.Method == "POST" {
		// another node is announcing themselves to us
		// if they are already in the Neighbors list, update as needed
		// TODO: this should use channels to make it concurrency safe, like Add
		if neighbor, ok := cluster.FindNeighborByUUID(r.FormValue("UUID")); ok {
			fmt.Println("found our neighbor")
			fmt.Println(neighbor.Nickname)
			if r.FormValue("Nickname") != "" {
				neighbor.Nickname = r.FormValue("Nickname")
			}
			if r.FormValue("Location") != "" {
				neighbor.Location = r.FormValue("Location")
			}
			if r.FormValue("BaseUrl") != "" {
				neighbor.BaseUrl = r.FormValue("BaseUrl")
			}
			if r.FormValue("Writeable") != "" {
				neighbor.Writeable = r.FormValue("Writeable") == "true"
			}
			neighbor.LastSeen = time.Now()
			// TODO: gossip enable by accepting the list of neighbors
			// from the client and merging that data in.
			// for now, just let it update its own entry

		} else {
			// otherwise, add them to the Neighbors list
			fmt.Println("adding neighbor")
			nd := models.NodeData{
				Nickname: r.FormValue("Nickname"),
				UUID:     r.FormValue("UUID"),
				BaseUrl:  r.FormValue("BaseUrl"),
				Location: r.FormValue("Location"),
			}
			if r.FormValue("Writeable") == "true" {
				nd.Writeable = true
			} else {
				nd.Writeable = false
			}
			nd.LastSeen = time.Now()
			cluster.AddNeighbor(nd)
		}
	}
	ar := AnnounceResponse{
		Nickname:  cluster.Myself.Nickname,
		UUID:      cluster.Myself.UUID,
		Location:  cluster.Myself.Location,
		Writeable: cluster.Myself.Writeable,
		BaseUrl:   cluster.Myself.BaseUrl,
		Neighbors: cluster.Neighbors,
	}
	b, err := json.Marshal(ar)
	if err != nil {
		fmt.Println("error:", err)
	}
	w.Write(b)
}

func FaviconHandler(w http.ResponseWriter, r *http.Request) {
	// just give it nothing to make it go away
	w.Write(nil)
}
