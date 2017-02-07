FROM joelchen/golang-vips

ADD . /go/App
ENV GOBIN=$GOPATH/bin
RUN cd /go/App && go get && go build main.go