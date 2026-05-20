FROM node:22-alpine
WORKDIR /workspace
EXPOSE 3200
CMD ["node", "-e", "require('node:http').createServer((req,res)=>{res.end('ok')}).listen(3200,'0.0.0.0')"]
