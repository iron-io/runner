FROM iron/dind

RUN apk update && apk upgrade && apk add --no-cache curl make git bzr mercurial go

RUN mkdir /go
ENV GOPATH /go
