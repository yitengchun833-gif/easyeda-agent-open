import {mkdir,readFile,writeFile,copyFile} from 'node:fs/promises';
import {dirname,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const here=dirname(fileURLToPath(import.meta.url));
const source=resolve(here,'../../knowledge/ui');
const target=resolve(here,'../iframe/knowledge');
await mkdir(target,{recursive:true});
// EasyEDA serves extension HTML through a blob URL; relative assets cannot resolve.
const [html,css,js]=await Promise.all(['index.html','style.css','app.js'].map(name=>readFile(resolve(source,name),'utf8')));
const bundled=html.replace('<link rel="stylesheet" href="style.css">',()=>'<style>'+css+'</style>').replace('<script src="app.js"></script>',()=>'<script>'+js.replace(/<\/script/gi,'<\\/script')+'</script>');
await writeFile(resolve(target,'index.html'),bundled);
await copyFile(resolve(source,'UPSTREAM-NOTICE'),resolve(target,'UPSTREAM-NOTICE'));
