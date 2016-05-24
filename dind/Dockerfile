FROM iron/base:edge

RUN echo '@community http://nl.alpinelinux.org/alpine/edge/community' >> /etc/apk/repositories
RUN apk update && apk upgrade \
    && apk add docker@community \
    && rm -rf /var/cache/apk/*

# RUN apk add openrc
# puts file in /etc/runlevels/...
# THIS DOESN'T WORK ANYMORE? 
# RUN rc-update add docker default
# CMD ["/sbin/rc", "default"]

COPY entrypoint.sh /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# If we don't want dind, we could have a separate img that doesn't start docker, in that case, remove the entrypoint things above and just use:
# ENTRYPOINT ["./runner"]

# USAGE: Add a CMD to your own Dockerfile to use this. 
# CMD ["./runner"]
