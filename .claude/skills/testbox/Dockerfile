# A Debian test machine for Finallyshell: systemd, sshd, sudo, and the
# tools the app's features drive. Test only: listen on loopback.
FROM debian:12
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server sudo ca-certificates curl wget gnupg \
      procps iproute2 iputils-ping net-tools lsof psmisc less vim-tiny nano \
      python3 socat ncat ufw nginx cron logrotate rsyslog fail2ban \
      tar gzip unzip zstd lrzsz sqlite3 jq git \
      debian-keyring debian-archive-keyring apt-transport-https \
    && apt-get clean && rm -rf /var/lib/apt/lists/* \
    && rm -f /lib/systemd/system/multi-user.target.wants/getty* \
    && systemctl mask getty.target systemd-logind.service \
    && systemctl disable nginx fail2ban \
    && mkdir -p /run/sshd
# Both accounts use disposable credentials; tester's sudo still prompts.
RUN sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/; s/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
    && useradd -m -s /bin/bash tester && usermod -aG sudo tester
# Ask systemd (PID 1) to shut down cleanly on docker stop.
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
