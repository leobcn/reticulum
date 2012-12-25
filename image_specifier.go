package main

// combination of field that uniquely specify an image
type ImageSpecifier struct {
	Hash      *Hash
	Size      string
	Extension string
}

func (i ImageSpecifier) MemcacheKey() string {
	return i.Hash.String() + "/" + i.Size + "/image" + i.Extension
}

func (i ImageSpecifier) sizedPath(upload_dir string) string {
	return resizedPath(i.fullSizePath(upload_dir), i.Size)
}

func (i ImageSpecifier) fullSizePath(upload_dir string) string {
	baseDir := upload_dir + i.Hash.AsPath()
	return baseDir + "/full" + i.Extension
}