# GoThumb

A very fast [golang](http://golang.org/) port of [thumbor](https://github.com/thumbor/thumbor).

## Features

- [x] Image Resizing via URL
- [x] HMAC url signing
- [x] Caching of resized images in S3
- [x] Parallel S3 cache downloads
- [x] Parallel S3 cache uploads
- [ ] Smart crop support
- [ ] Parallel source file fetching
- [ ] Other storage engines
- [ ] Tests
- [x] Unsafe mode


- [x] Option to save file local (added by @totorokk 07/21/2017, see config below &#11015;)
```
[server]
static = "http://localhost:3001/gothumb/static/"
local = true
```
