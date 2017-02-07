#!/bin/bash

docker build -t ihealthlab/gothumb-build .

docker run -d --name=gothumb-build ihealthlab/gothumb-build /bin/sh /go/bin/App

docker cp gothumb-build:/go/bin/App .

docker rm -f gothumb-build

docker build -t ihealthlab/gothumb -f Dockerfile.scratch .