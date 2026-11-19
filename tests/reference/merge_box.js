// node merge_box.js base.smap donor.smap out.smap x0 x1 y0 y1 : inside the box take walls, rssi and feature lines from donor
const fs=require('fs');const [bF,dF,oF,x0,x1,y0,y1]=process.argv.slice(2);const X0=+x0,X1=+x1,Y0=+y0,Y1=+y1;
const b=JSON.parse(fs.readFileSync(bF,'utf8')),d=JSON.parse(fs.readFileSync(dF,'utf8'));
const inB=(x,y)=>x>=X0&&x<=X1&&y>=Y0&&y<=Y1;const rep={};
for(const k of ['normalPosList','rssiPosList']){const keep=b[k].filter(p=>!inB(p.x||0,p.y||0));const add=d[k].filter(p=>inB(p.x||0,p.y||0));rep[k]={removed:b[k].length-keep.length,added:add.length};b[k]=keep.concat(add);}
const mid=l=>[((l.line.startPos.x||0)+(l.line.endPos.x||0))/2,((l.line.startPos.y||0)+(l.line.endPos.y||0))/2];
{const keep=b.advancedLineList.filter(l=>!inB(...mid(l)));const add=d.advancedLineList.filter(l=>inB(...mid(l)));rep.featureLines={removed:b.advancedLineList.length-keep.length,added:add.length};b.advancedLineList=keep.concat(add);}
fs.writeFileSync(oF,JSON.stringify(b));console.log(JSON.stringify(rep));
