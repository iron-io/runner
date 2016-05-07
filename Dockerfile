FROM iron/base:edge

RUN echo '@community http://nl.alpinelinux.org/alpine/edge/community' >> /etc/apk/repositories
RUN apk update && apk upgrade \
    && apk add docker@community \
    && rm -rf /var/cache/apk/*

# puts file in /etc/runlevels/...
# THIS DOESN'T WORK ANYMORE? rc-update not found. Maybe removed from image?
# RUN rc-update add docker default
# CMD ["/sbin/rc", "default"]

WORKDIR /app
ADD runner /app

ENTRYPOINT ["./runner"]
