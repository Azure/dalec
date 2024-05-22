#!/usr/bin/python3

import sys
import os
import socket
from socketserver import TCPServer, StreamRequestHandler

class Handler(StreamRequestHandler):
    def handle(self):
        # self.request is the TCP socket connected to the client
        self.data = self.request.recv(1024).strip()
        print("Received from {}:".format(self.client_address[0]))
        print(type(self.data))
        return_msg = "Received: {}".format(self.data)
        self.request.sendall(bytes(return_msg.encode('utf-8')))

class Server(TCPServer):
    SYSTEMD_FD = 3
    def __init__(self, address, handler_cls):
        # bind and activate must be false because systemd will handle this
        TCPServer.__init__(self, address, handler_cls, bind_and_activate=False)
        self.socket = socket.fromfd(self.SYSTEMD_FD, self.address_family, self.socket_type)
        
if __name__ == "__main__":
    server = Server(("localhost", 8080), Handler)
    server.serve_forever()
