FROM gitpod/workspace-full:2023-07-17-21-17-42

RUN curl -L https://fly.io/install.sh | sh \
 && echo 'export FLYCTL_INSTALL="/home/gitpod/.fly"' >> ~/.bash_profile \
 && echo 'export PATH="$FLYCTL_INSTALL/bin:$PATH"' >> ~/.bash_profile
