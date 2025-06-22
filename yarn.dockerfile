FROM  mcr.microsoft.com/cbl-mariner/base/core:2.0

RUN tdnf install -y \
    nodejs \
    npm \
    ca-certificates

RUN npm install -g yarn
RUN yarn config set yarn-offline-mirror /yarn-dalec-cache-npm