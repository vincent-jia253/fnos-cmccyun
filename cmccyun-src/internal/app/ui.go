package app

// indexHTML is the whole management console. It is served from the binary so
// the app has no runtime asset dependencies.
//
// 注意：这是一段 Go 原始字符串，里面不能出现反引号 —— JS 里拼字符串一律用
// 单/双引号加号连接，别用模板字符串。
const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>移动云盘 · 挂载控制台</title>
<meta name="author" content="vincent" />
<meta name="description" content="把中国移动云盘（139 云盘）挂载成飞牛 fnOS 本地文件夹 · by vincent" />
<link rel="icon" href="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAzMiAzMiI+PHJlY3Qgd2lkdGg9IjMyIiBoZWlnaHQ9IjMyIiByeD0iOCIgZmlsbD0iIzI1NjNlYiIvPjxwYXRoIGQ9Ik0xMC4yIDIzaDEyLjNhNC4zIDQuMyAwIDAgMCAuNS04LjU3IDYuMiA2LjIgMCAwIDAtMTEuOS0xLjJBNC40IDQuNCAwIDAgMCAxMC4yIDIzWiIgZmlsbD0iI2ZmZiIvPjwvc3ZnPg==" />
<style>
  :root{
    color-scheme:light dark;
    --bg:#f4f6fa; --bg2:#eef2f8; --card:#ffffff; --card2:#f8fafc;
    --fg:#0f172a; --muted:#6b7280; --faint:#98a2b3; --line:#e4e8ef;
    --accent:#2563eb; --accent2:#1d4ed8; --accent-soft:rgba(37,99,235,.10);
    --ok:#10b981; --ok-soft:rgba(16,185,129,.12);
    --warn:#f59e0b; --warn-soft:rgba(245,158,11,.13);
    --err:#ef4444; --err-soft:rgba(239,68,68,.12);
    --shadow:0 1px 2px rgba(16,24,40,.05), 0 12px 32px -20px rgba(16,24,40,.35);
    --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;
  }
  @media (prefers-color-scheme: dark){
    :root{
      --bg:#0d1117; --bg2:#0a0e14; --card:#151b23; --card2:#1b222c;
      --fg:#e8edf4; --muted:#98a4b3; --faint:#6c7a8c; --line:#252d38;
      --accent:#3b82f6; --accent2:#60a5fa; --accent-soft:rgba(59,130,246,.16);
      --ok:#34d399; --ok-soft:rgba(52,211,153,.14);
      --warn:#fbbf24; --warn-soft:rgba(251,191,36,.14);
      --err:#f87171; --err-soft:rgba(248,113,113,.14);
      --shadow:0 1px 2px rgba(0,0,0,.45), 0 16px 40px -24px rgba(0,0,0,.95);
    }
  }
  *{box-sizing:border-box;}
  html,body{height:100%;}
  body{
    margin:0; background:var(--bg); color:var(--fg);
    font:14.5px/1.62 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
    -webkit-font-smoothing:antialiased;
  }
  a{color:var(--accent);}
  h1,h2,h3{margin:0;}

  /* ---------- 顶栏 ---------- */
  .topbar{
    position:sticky; top:0; z-index:30;
    display:flex; align-items:center; gap:14px; flex-wrap:wrap;
    padding:12px 20px; background:color-mix(in srgb,var(--card) 88%,transparent);
    backdrop-filter:saturate(160%) blur(10px);
    border-bottom:1px solid var(--line);
  }
  .brand{display:flex; align-items:center; gap:11px; min-width:0;}
  .logo{
    width:38px; height:38px; border-radius:11px; flex:0 0 auto;
    display:grid; place-items:center; color:#fff;
    background:linear-gradient(140deg,#3b82f6,#1d4ed8 55%,#1e40af);
    box-shadow:0 6px 16px -8px rgba(37,99,235,.9);
  }
  .brand .t{font-size:16px; font-weight:650; letter-spacing:.2px;}
  .brand .s{font-size:12px; color:var(--muted); margin-top:-2px;}
  /* 撑开用的占位元素。注意：不要用 .sp —— .sp 是「转圈圈」的加载指示器，
     两者混用会让占位元素变成一个巨大的旋转圆弧（v1.3.0 就是这样被用户抓到的）。 */
  .grow{flex:1 1 auto;}
  .topbar .grow{flex:1 1 auto;}
  .chips{display:flex; align-items:center; gap:8px; flex-wrap:wrap;}

  .chip{
    display:inline-flex; align-items:center; gap:6px;
    font-size:12.5px; padding:5px 11px; border-radius:999px;
    border:1px solid var(--line); background:var(--card2); color:var(--muted);
    white-space:nowrap;
  }
  .chip b{color:var(--fg); font-weight:560;}
  /* 顶栏的账号切换器：形状跟 chip 一致，宽度跟着内容走 */
  .acctsel{
    width:auto; min-width:132px; max-width:220px;
    font-size:12.5px; padding:6px 10px; border-radius:999px;
    background:var(--card2); color:var(--fg);
  }
  /* 带悬停说明的元素：给个「可以问」的指针提示 */
  .chip[title]{cursor:help;}
  .hint[title]{cursor:help;}
  .hero .meta span[title]{cursor:help; text-decoration:underline dotted; text-underline-offset:3px; text-decoration-color:var(--line);}
  .chip.ok{color:var(--ok); border-color:color-mix(in srgb,var(--ok) 40%,transparent); background:var(--ok-soft);}
  .chip.warn{color:var(--warn); border-color:color-mix(in srgb,var(--warn) 40%,transparent); background:var(--warn-soft);}
  .chip.err{color:var(--err); border-color:color-mix(in srgb,var(--err) 40%,transparent); background:var(--err-soft);}
  .dot{width:7px; height:7px; border-radius:50%; background:currentColor; flex:0 0 auto;}
  .chip.live .dot{animation:pulse 2s ease-out infinite;}
  @keyframes pulse{0%{box-shadow:0 0 0 0 currentColor; opacity:1;} 70%{box-shadow:0 0 0 6px transparent; opacity:.85;} 100%{box-shadow:0 0 0 0 transparent; opacity:1;}}

  .iconbtn{
    width:36px; height:36px; padding:0; border-radius:10px;
    display:grid; place-items:center; background:var(--card2);
  }
  .iconbtn.spin svg{animation:rot .9s linear infinite;}
  @keyframes rot{to{transform:rotate(360deg);}}

  /* ---------- 布局 ---------- */
  .wrap{max-width:1060px; margin:0 auto; padding:20px 20px 56px;}
  .stack{display:grid; gap:16px;}

  .card{
    background:var(--card); border:1px solid var(--line);
    border-radius:15px; box-shadow:var(--shadow);
  }
  .card>.hd{
    display:flex; align-items:center; gap:10px; flex-wrap:wrap;
    padding:15px 18px 0;
  }
  .card>.hd h2{font-size:15px; font-weight:620;}
  .card>.hd .sub{font-size:12.5px; color:var(--muted);}
  .card>.bd{padding:15px 18px 18px;}

  /* ---------- 状态卡 ---------- */
  .hero{padding:20px; display:flex; gap:22px; align-items:flex-start; flex-wrap:wrap;}
  .hero .left{flex:1 1 380px; min-width:0;}
  .hero .right{display:flex; gap:10px; flex-wrap:wrap; align-items:center; padding-top:2px;}
  .state{display:flex; align-items:center; gap:11px;}
  .sdot{
    width:13px; height:13px; border-radius:50%; flex:0 0 auto;
    background:var(--faint); box-shadow:0 0 0 5px var(--accent-soft);
  }
  .sdot.on{background:var(--ok); box-shadow:0 0 0 5px var(--ok-soft); animation:pulse 2.4s ease-out infinite;}
  .state .txt{font-size:22px; font-weight:660; letter-spacing:.3px; line-height:1.25;}
  .state .txt.off{color:var(--muted);}
  .pathrow{display:flex; align-items:center; gap:8px; margin-top:10px; flex-wrap:wrap;}
  .path{
    font-family:var(--mono); font-size:13px; padding:6px 11px; border-radius:9px;
    background:var(--card2); border:1px solid var(--line); color:var(--fg);
    max-width:100%; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;
  }
  .meta{display:flex; gap:7px 16px; flex-wrap:wrap; margin-top:12px; font-size:12.8px; color:var(--muted);}
  .meta span b{color:var(--fg); font-weight:560;}
  .hero .hint{margin-top:11px; font-size:12.8px; color:var(--muted);}

  .strip{
    display:flex; gap:9px; align-items:flex-start; margin-top:13px;
    padding:10px 13px; border-radius:11px; font-size:13px;
    background:var(--warn-soft); color:var(--warn);
    border:1px solid color-mix(in srgb,var(--warn) 35%,transparent);
  }
  .strip.err{background:var(--err-soft); color:var(--err); border-color:color-mix(in srgb,var(--err) 35%,transparent);}
  .strip.ok{background:var(--ok-soft); color:var(--ok); border-color:color-mix(in srgb,var(--ok) 32%,transparent);}

  /* ---------- 按钮 / 表单 ---------- */
  button{
    font:inherit; font-size:13.5px; cursor:pointer;
    display:inline-flex; align-items:center; justify-content:center; gap:7px;
    padding:9px 15px; border-radius:10px;
    border:1px solid var(--line); background:var(--card); color:var(--fg);
    transition:transform .06s ease, background .15s ease, border-color .15s ease, box-shadow .15s ease;
    white-space:nowrap;
  }
  button:hover:not(:disabled){border-color:color-mix(in srgb,var(--accent) 45%,var(--line)); background:var(--card2);}
  button:active:not(:disabled){transform:translateY(1px);}
  button:disabled{opacity:.55; cursor:not-allowed;}
  button.primary{
    background:linear-gradient(180deg,var(--accent),var(--accent2));
    border-color:transparent; color:#fff; box-shadow:0 6px 18px -12px rgba(37,99,235,.95);
  }
  button.primary:hover:not(:disabled){filter:brightness(1.06); background:linear-gradient(180deg,var(--accent),var(--accent2));}
  button.ghost{background:transparent;}
  button.danger{color:var(--err); border-color:color-mix(in srgb,var(--err) 42%,var(--line));}
  button.danger:hover:not(:disabled){background:var(--err-soft); border-color:var(--err);}
  button.sm{padding:6px 11px; font-size:12.5px; border-radius:9px;}
  .sp{
    width:13px; height:13px; border-radius:50%; flex:0 0 auto;
    border:2px solid currentColor; border-right-color:transparent; border-top-color:transparent;
    animation:rot .7s linear infinite; opacity:.9;
  }
  /* 只在按钮里以 <i class="sp"> 出现；占位请用 .grow */
  button .sp, .btn .sp{display:inline-block; vertical-align:-1px;}

  label{display:block; font-size:12.5px; color:var(--muted); margin-bottom:6px;}
  input,select{
    width:100%; font:inherit; font-size:13.5px; color:var(--fg);
    padding:9px 11px; border-radius:10px; background:var(--card);
    border:1px solid var(--line); outline:none; transition:border-color .15s, box-shadow .15s;
  }
  input:focus,select:focus{border-color:var(--accent); box-shadow:0 0 0 3px var(--accent-soft);}
  input[type=checkbox]{width:17px; height:17px; accent-color:var(--accent); cursor:pointer;}
  input:disabled{background:var(--card2); color:var(--muted);}
  .grid{display:grid; grid-template-columns:repeat(auto-fit,minmax(210px,1fr)); gap:13px;}
  .row{display:flex; align-items:center; gap:9px; flex-wrap:wrap;}
  .row.nowrap{flex-wrap:nowrap;}
  .checkline{display:flex; align-items:center; gap:9px; font-size:13.5px; color:var(--fg);}
  .actions{display:flex; gap:10px; flex-wrap:wrap; margin-top:16px;}
  .hint{color:var(--muted); font-size:12.6px;}
  .hint code,code.k{
    font-family:var(--mono); font-size:12px; padding:2px 6px; border-radius:6px;
    background:var(--accent-soft); color:var(--accent); border:1px solid color-mix(in srgb,var(--accent) 22%,transparent);
  }
  .msg{font-size:13px; min-height:19px; margin-top:10px; color:var(--muted);}
  .msg.ok{color:var(--ok);} .msg.err{color:var(--err);}
  ol.steps{margin:0; padding-left:20px; color:var(--muted); font-size:13px;}
  ol.steps li{margin-bottom:5px;}
  ol.steps code{font-family:var(--mono); font-size:12px; background:var(--card2); padding:1px 5px; border-radius:5px; border:1px solid var(--line);}
  details.adv{margin-top:15px; border:1px solid var(--line); border-radius:12px; background:var(--card2); overflow:hidden;}
  details.adv>summary{padding:11px 14px; cursor:pointer; font-size:13px; color:var(--muted); user-select:none; list-style:none;}
  details.adv>summary::-webkit-details-marker{display:none;}
  details.adv>summary::before{content:"▸ "; color:var(--faint);}
  details.adv[open]>summary::before{content:"▾ ";}
  details.adv .inner{padding:4px 14px 15px;}

  /* ---------- 扫码登录 ---------- */
  .qrwrap{display:flex; gap:18px; align-items:flex-start; flex-wrap:wrap;}
  /* 二维码底色固定白：深色主题下也要能扫得动 */
  .qrbox{
    width:216px; height:216px; flex:0 0 auto; padding:8px; box-sizing:border-box;
    border-radius:13px; border:1px solid var(--line); background:#fff;
    display:grid; place-items:center; overflow:hidden;
  }
  .qrbox img{width:100%; height:100%; object-fit:contain; display:none; image-rendering:pixelated;}
  .qrbox img.on{display:block;}
  .qrph{text-align:center; color:#98a2b3; font-size:12.5px; line-height:1.6;}
  .qrph .qico{font-size:30px; line-height:1; margin-bottom:9px; opacity:.4;}
  .qrph.off{display:none;}
  .qrside{flex:1 1 320px; min-width:0;}
  .tag{
    font-size:11.5px; font-weight:600; color:var(--accent); background:var(--accent-soft);
    border:1px solid color-mix(in srgb,var(--accent) 22%,transparent);
    padding:2px 8px; border-radius:999px; white-space:nowrap;
  }
  .cmp th:first-child, .cmp td:first-child{color:var(--muted); white-space:nowrap;}
  .cmp td{vertical-align:top; line-height:1.55;}
  .cmp td b{font-weight:560;}

  /* ---------- 页脚 ---------- */
  .pagefoot{
    display:flex; gap:6px 18px; align-items:center; flex-wrap:wrap;
    margin:22px 2px 0; padding-top:13px; border-top:1px solid var(--line);
    font-size:12.3px; color:var(--muted);
  }
  .pagefoot b{color:var(--fg); font-weight:560;}
  .pagefoot a{color:var(--accent); text-decoration:none;}
  .pagefoot a:hover{text-decoration:underline;}

  /* ---------- 标签页 ---------- */
  .tabs{display:flex; gap:5px; padding:5px; border-radius:13px; background:var(--card); border:1px solid var(--line); box-shadow:var(--shadow); overflow-x:auto;}
  .tab{
    border:none; background:transparent; color:var(--muted); font-size:13.5px;
    padding:9px 15px; border-radius:9px; flex:0 0 auto;
  }
  .tab:hover:not(.act){background:var(--card2); border-color:transparent;}
  .tab.act{background:var(--accent-soft); color:var(--accent); font-weight:600;}
  .panel{display:none;} .panel.act{display:block;}

  /* ---------- 表格 / 列表 ---------- */
  table{width:100%; border-collapse:collapse; font-size:13.5px;}
  th{
    text-align:left; font-weight:560; font-size:12px; color:var(--muted);
    padding:8px 10px; border-bottom:1px solid var(--line); white-space:nowrap;
  }
  td{padding:9px 10px; border-bottom:1px solid color-mix(in srgb,var(--line) 65%,transparent);}
  tr:last-child td{border-bottom:none;}
  tbody tr:hover td{background:var(--card2);}
  .scroll{max-height:330px; overflow:auto; border:1px solid var(--line); border-radius:12px;}
  .empty{padding:22px; text-align:center; color:var(--faint); font-size:13px;}
  .fname{display:flex; align-items:center; gap:9px; min-width:0;}
  .fname span{overflow:hidden; text-overflow:ellipsis; white-space:nowrap;}
  .linklike{color:var(--accent); cursor:pointer; font-weight:520;}
  .linklike:hover{text-decoration:underline;}
  .crumb{font-family:var(--mono); font-size:12.5px; color:var(--muted);}

  pre{
    margin:0; padding:14px; border-radius:12px; font-size:12.2px; line-height:1.62;
    background:#0b1017; color:#c7d2de; border:1px solid #1c2530;
    font-family:var(--mono); max-height:420px; overflow:auto; white-space:pre-wrap; word-break:break-all;
  }
  @media (prefers-color-scheme: light){ pre{background:#0f172a; color:#d7e0ea;} }

  /* ---------- toast ---------- */
  #toasts{position:fixed; right:16px; bottom:16px; z-index:80; display:flex; flex-direction:column; gap:10px; max-width:min(420px,calc(100vw - 32px));}
  .toast{
    display:flex; gap:10px; align-items:flex-start;
    padding:12px 14px; border-radius:12px; font-size:13.3px;
    background:var(--card); border:1px solid var(--line); box-shadow:var(--shadow);
    animation:tin .22s cubic-bezier(.2,.9,.3,1.2);
  }
  .toast .mk{width:3px; align-self:stretch; border-radius:3px; background:var(--accent); flex:0 0 auto;}
  .toast.ok .mk{background:var(--ok);} .toast.err .mk{background:var(--err);} .toast.warn .mk{background:var(--warn);}
  .toast .tx{flex:1; min-width:0; word-break:break-word;}
  .toast .x{cursor:pointer; color:var(--faint); background:none; border:none; padding:0 2px; font-size:15px; line-height:1;}
  @keyframes tin{from{opacity:0; transform:translateY(10px) scale(.98);} to{opacity:1; transform:none;}}

  /* ---------- 位置选择器 ---------- */
  .modal{position:fixed; inset:0; background:rgba(9,13,20,.55); backdrop-filter:blur(3px); display:flex; align-items:center; justify-content:center; z-index:60; padding:16px;}
  .modal .box{
    background:var(--card); border:1px solid var(--line); border-radius:16px;
    width:min(980px,100%); max-height:92vh; overflow:auto; padding:20px; box-shadow:var(--shadow);
  }
  .modal .box h3{font-size:16px; font-weight:640;}
  .modal .box .sub{font-size:12.5px; color:var(--muted); margin:5px 0 12px;}
  .pickwrap{display:flex; border:1px solid var(--line); border-radius:12px; overflow:hidden; background:var(--card);}
  .picktree{width:41%; border-right:1px solid var(--line); max-height:340px; overflow:auto; background:var(--card2);}
  .picklist{flex:1; max-height:340px; overflow:auto; min-width:0;}
  .picker-head{
    position:sticky; top:0; z-index:2; background:var(--card2);
    border-bottom:1px solid var(--line); padding:8px 12px;
    font-size:12.3px; color:var(--muted); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;
  }
  .picklist .picker-head{background:var(--card);}
  .tnode{display:flex; align-items:center; gap:2px; padding:4px 10px 4px 0; font-size:13.3px; cursor:pointer; white-space:nowrap; border-radius:0;}
  .tnode:hover{background:color-mix(in srgb,var(--accent) 9%,transparent);}
  .tnode.sel{background:var(--accent-soft); color:var(--accent); font-weight:560;}
  .tnode .caret{flex:0 0 16px; width:16px; text-align:center; color:var(--faint); font-size:10px;}
  .tnode .tname{overflow:hidden; text-overflow:ellipsis;}
  .tnode .tag{font-size:10.5px; margin-left:6px; padding:1px 6px; border-radius:999px; background:var(--ok-soft); color:var(--ok);}
  .tnode .tag.sys{background:var(--card); color:var(--faint); border:1px solid var(--line);}
  .lrow{display:flex; align-items:center; gap:10px; padding:7px 12px; font-size:13.3px; cursor:pointer;}
  .lrow:hover{background:color-mix(in srgb,var(--accent) 9%,transparent);}
  .lrow .lname{flex:1; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;}
  .lrow .lmeta{color:var(--muted); font-size:12px; white-space:nowrap;}
  .lrow.up{color:var(--muted);}
  .qlink{font-size:12.3px; padding:6px 11px; border-radius:999px; background:var(--card2);}
  .modal .foot{display:flex; gap:10px; align-items:center; flex-wrap:wrap; margin-top:14px;}
  .modal .foot .sel{flex:1 1 260px; min-width:0; font-size:12.7px; color:var(--muted);}
  .modal .foot .sel.warn{color:var(--warn);}
  .modal .foot .sel.ok{color:var(--ok);}
  .field{margin-top:12px;}

  @media (max-width:640px){
    .wrap{padding:14px 13px 48px;}
    .topbar{padding:10px 13px;}
    .hero{padding:16px; gap:16px;}
    .state .txt{font-size:19px;}
    .pickwrap{flex-direction:column;}
    .picktree{width:100%; max-height:210px; border-right:none; border-bottom:1px solid var(--line);}
  }
</style>
</head>
<body>

<header class="topbar">
  <div class="brand">
    <div class="logo">
      <svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 15.5a4.5 4.5 0 1 0 0-9 4.5 4.5 0 0 0 0 9Z" opacity=".0"/>
        <path d="M7 18h9.5a4 4 0 0 0 .4-7.98A5.5 5.5 0 0 0 6.2 9.4 3.8 3.8 0 0 0 7 18Z"/>
      </svg>
    </div>
    <div>
      <div class="t">移动云盘</div>
      <div class="s">139 云盘 · 挂载到飞牛本地文件系统</div>
    </div>
  </div>
  <div class="grow"></div>
  <div class="chips">
    <select id="acctSel" class="acctsel" style="display:none" title="切换账号：下面每个页签都只作用在当前账号上"></select>
    <span id="badgeMount" class="chip"><i class="dot"></i><b>状态读取中…</b></span>
    <span id="badgeAcct" class="chip">未授权</span>
    <span id="badgeAuth" class="chip">-</span>
    <span id="badgeVer" class="chip">-</span>
    <button id="btnRefreshAll" class="iconbtn" title="刷新状态">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6"/>
      </svg>
    </button>
  </div>
</header>

<main class="wrap">
  <div class="stack">

    <!-- ===== 状态卡：已挂载 / 未挂载 ===== -->
    <section class="card hero">
      <div class="left">
        <div class="state">
          <i id="heroDot" class="sdot"></i>
          <div id="heroState" class="txt off">状态读取中…</div>
        </div>
        <div class="pathrow">
          <span class="path" id="heroPath">—</span>
          <button id="btnCopyPath" class="sm ghost" title="复制挂载路径">复制</button>
        </div>
        <div class="meta" id="heroMeta"></div>
        <div class="hint" id="heroHint"></div>
        <div id="lastErr"></div>
      </div>
      <div class="right">
        <button id="btnMount" class="primary">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m0-12 4 4m-4-4-4 4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>
          挂载
        </button>
        <button id="btnRemount">重新挂载</button>
        <button id="btnUnmount" class="danger">卸载</button>
      </div>
    </section>

    <nav class="tabs" id="tabs">
      <button class="tab act" data-tab="mount">挂载设置</button>
      <button class="tab" data-tab="accounts">账号</button>
      <button class="tab" data-tab="auth">授权</button>
      <button class="tab" data-tab="browse">云盘浏览</button>
      <button class="tab" data-tab="dav">远程挂载</button>
      <button class="tab" data-tab="strm">影视 STRM</button>
      <button class="tab" data-tab="logs">运行日志</button>
    </nav>

    <!-- ===== 挂载设置 ===== -->
    <section class="panel act" id="tab-mount">
      <div class="card">
        <div class="hd"><h2>挂载位置</h2><span class="sub">挂到哪里都行，挂进个人空间后「文件」里就能直接看到</span></div>
        <div class="bd">
          <label>目录路径</label>
          <div class="row nowrap">
            <input id="f_mount" list="shareList" placeholder="点右边「浏览…」选一个位置" spellcheck="false" />
            <button id="btnPick" style="flex:0 0 auto">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.4.6L11.4 7H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/></svg>
              浏览…
            </button>
          </div>
          <datalist id="shareList"></datalist>
          <div class="hint" style="margin-top:7px">
            推荐挂在个人空间下的子文件夹，例如 <code class="k">/vol4/1000/移动云盘</code>（1000 是登录账号的 uid）。
            挂在个人空间根目录会遮住你原有的文件。
          </div>

          <details class="adv">
            <summary>高级参数（属主、缓存、只读、端口…）</summary>
            <div class="inner">
              <div class="grid">
                <div>
                  <label>只读挂载</label>
                  <div class="checkline" style="height:38px"><input id="f_ro" type="checkbox" /> <span>开启后所有写操作返回只读错误</span></div>
                </div>
                <div><label>文件属主 UID</label><input id="f_uid" type="number" min="0" /></div>
                <div><label>文件属主 GID</label><input id="f_gid" type="number" min="0" /></div>
                <div><label>目录缓存（秒）</label><input id="f_ttl" type="number" min="1" /></div>
                <div><label>上传分片（MB）</label><input id="f_part" type="number" min="1" max="5120" /></div>
                <div><label>statfs 容量（GB）</label><input id="f_cap" type="number" min="1" /></div>
                <div>
                  <label>客户端类型标识</label>
                  <select id="f_ctype">
                    <option value="13">13 · PC 客户端（单文件最大 500 GB）</option>
                    <option value="9">9 · 网页（单文件最大 50 GB）</option>
                    <option value="2">2 · App（单文件最大 50 GB）</option>
                  </select>
                </div>
                <div>
                  <label>开机自动挂载</label>
                  <div class="checkline" style="height:38px"><input id="f_auto" type="checkbox" /> <span>随应用启动时挂上</span></div>
                </div>
                <div><label>管理端口（重启生效）</label><input id="f_port" type="number" disabled /></div>
              </div>
            </div>
          </details>

          <div class="actions">
            <button id="btnSave" class="primary">保存设置</button>
            <button id="btnPick2">选择位置…</button>
          </div>
          <div id="msgCfg" class="msg"></div>

          <div class="hd" style="margin-top:22px;padding-top:16px;border-top:1px solid rgba(127,127,127,.18)">
            <h2 style="font-size:15px">控制台访问密码</h2>
            <span class="chip warn" id="webPassDefault" style="display:none;padding:2px 8px">还在用默认密码</span>
            <span class="sub">打开控制台要输密码；默认 <b>admin / admin</b>，改完即时生效</span>
          </div>
          <div class="grid" style="margin-top:10px">
            <div><label>当前密码</label><input id="webPassCur" type="password" autocomplete="current-password" placeholder="默认 admin" /></div>
            <div><label>新密码（至少 4 位）</label><input id="webPassNew" type="password" autocomplete="new-password" /></div>
            <div><label>再输一次新密码</label><input id="webPassNew2" type="password" autocomplete="new-password" /></div>
            <div><label>登录名</label><input id="webUser" spellcheck="false" placeholder="admin" /></div>
          </div>
          <div class="actions">
            <button id="btnWebPass" class="primary">修改密码 / 登录名</button>
            <button id="btnLogout" class="ghost">退出登录</button>
          </div>
          <div id="msgWebPass" class="msg"></div>
          <div class="hint" style="margin-top:8px">
            <code class="k">/dav/</code>（WebDAV）与 STRM 直链不受这个密码影响：它们各自有自己的账号和口令。<br>
            忘了密码：删掉 <code class="k">config.json</code> 里的 <code class="k">web_password</code> 再重启，回到默认 admin。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 授权 ===== -->
    <section class="panel" id="tab-auth">

      <!-- ① 扫码登录：推荐路径 -->
      <div class="card">
        <div class="hd">
          <h2>扫码登录</h2><span class="tag">推荐</span>
          <span class="sub">手机扫一下就行，不用开 F12 抓包</span>
        </div>
        <div class="bd">
          <div class="qrwrap">
            <div class="qrbox">
              <img id="qrImg" alt="登录二维码" src="" />
              <div id="qrPh" class="qrph">
                <div class="qico">▣</div>
                <div>还没有二维码<br />点右侧「生成二维码」</div>
              </div>
            </div>
            <div class="qrside">
              <ol class="steps">
                <li>手机打开<b>中国移动云盘</b>App（139 邮箱 / 移动云盘都可以），确认已登录。</li>
                <li>用 App 里的「扫一扫」扫左边这个二维码。</li>
                <li>手机上点<b>确认登录</b>；这边会自动保存授权，并且<b>顺手把云盘挂载好</b>。</li>
              </ol>
              <div class="actions">
                <button id="btnQrStart" class="primary">生成二维码</button>
                <button id="btnQrStop">停止</button>
              </div>
              <div id="qrMsg" class="msg"></div>
              <div class="hint" style="margin-top:4px">
                二维码 5 分钟有效、扫完即作废，是<b>一次性临时凭据</b>，不会保存到磁盘上。
                服务端只保留一张内存里的会话表，进程重启就得重扫——这是故意的。
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- ② 手工粘贴：备选路径 -->
      <div class="card">
        <div class="hd">
          <h2>手工粘贴 Authorization</h2>
          <span class="sub">备选：扫码用不了的时候（手机不在身边、脚本批量部署）</span>
        </div>
        <div class="bd">
          <ol class="steps">
            <li>浏览器打开 <code>https://yun.139.com/w/</code> 并登录（用电脑版网页）。</li>
            <li>按 F12 打开开发者工具 → Network，筛选 <code>file/list</code> 或 <code>hcy</code>。</li>
            <li>点开任意一条请求，在 Request Headers 里复制 <code>Authorization</code> 的值（<code>Basic </code> 后面的部分即可）。</li>
          </ol>
          <div class="field">
            <label>Authorization</label>
            <input id="f_auth" type="password" placeholder="留空表示不修改" autocomplete="off" spellcheck="false" />
          </div>
          <div class="hint" style="margin:-4px 0 10px">
            粘贴时不用挑格式：整行 <code class="k">Authorization: Basic xxxxx</code>、只有 <code class="k">Basic xxxxx</code>、
            或直接那一长串 base64 都认；被编辑器折成几行也认（我们会去掉空白）。
          </div>
          <details style="margin:0 0 12px">
            <summary style="cursor:pointer;font-size:13px">扫码扫不上？用下面的书签两步拿到授权</summary>
            <ol class="hint" style="margin:8px 0 0 18px;line-height:1.9">
              <li>在这台电脑的浏览器里登录 <a href="https://yun.139.com/w/" target="_blank" rel="noreferrer">yun.139.com</a>（要授权的那个账号，手机上「我的 → 账号」能看到是哪个）。</li>
              <li>把下面这段存成书签，在 139 页面上点一下（或在地址栏粘一次后回车）：它会从浏览器本地把整条 Authorization 复制走。</li>
              <li>回到本页，粘进上面的 Authorization 框 → 「保存授权」。</li>
            </ol>
            <div class="row" style="margin-top:8px;align-items:flex-start">
              <code id="bmCode" class="k" style="flex:1 1 auto;word-break:break-all;font-size:11px;line-height:1.7">javascript:(function(){var a=localStorage.getItem("authorization")||"";if(!a){alert("没找到授权：请先在这个浏览器里登录 yun.139.com");return;}var b=a.replace(/^Authorization:\s*/i,"").replace(/^Basic\s+/i,"").trim();function done(){alert("已复制 "+b.length+" 个字符，回到 cmccyun 控制台「授权」页粘贴即可");}if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(b).then(done,function(){prompt("复制这段 Authorization：",b);});}else{prompt("复制这段 Authorization：",b);}})()</code>
              <button id="btnCopyBm" class="sm" style="flex:0 0 auto">复制代码</button>
            </div>
            <div class="hint" style="margin-top:6px">
              原理：网页版把整条 <code class="k">Authorization</code>（<code class="k">base64("pc:账号:令牌")</code>）明文存在
              <code class="k">localStorage.authorization</code> 里，这段小程序只做「读出来 + 去掉 Basic 前缀 + 复制」，不外发任何数据。
            </div>
          </details>
          <div class="actions">
            <button id="btnSaveAuth" class="primary">保存授权</button>
            <button id="btnRefreshAuth" title="立即向云盘服务端续期一次：成功后续期天数回到约 30 天，并把新授权写回配置">刷新令牌</button>
            <button id="btnTest">测试连接</button>
          </div>
          <div id="msgAuth" class="msg"></div>
          <div class="hint" id="authInfo" style="margin-top:6px" title=""></div>
        </div>
      </div>

      <!-- ③ 两种方式怎么选 + 有效期说明 -->
      <div class="card">
        <div class="hd"><h2>两种登录方式怎么选</h2><span class="sub">两条路拿到的是同一种凭据，效果完全一样</span></div>
        <div class="bd">
          <table class="cmp">
            <thead>
              <tr><th></th><th>扫码登录</th><th>手工粘贴</th></tr>
            </thead>
            <tbody>
              <tr><td>要做几步</td><td><b>点一下 → 手机扫 → 确认</b></td><td>开网页登录 → F12 → 复制请求头 → 粘贴</td></tr>
              <tr><td>电脑上要不要</td><td>只要这个控制台能打开</td><td>要能打开并登录 139 网页版</td></tr>
              <tr><td>拿到的凭据</td><td>同一条 <code>base64("pc:账号:令牌")</code></td><td>同一条 <code>base64("pc:账号:令牌")</code></td></tr>
              <tr><td>有效期</td><td>约 30 天，应用自动续期</td><td>约 30 天，应用自动续期</td></tr>
              <tr><td>最合适</td><td><b>日常第一次装、换号、换手机</b></td><td>扫码被网络策略拦、脚本批量安装、远程帮人装</td></tr>
            </tbody>
          </table>
          <div class="hint" style="margin-top:11px">
            两个入口最后都走同一套逻辑：写进配置 → 建立客户端 → 自动续期 → 挂载。所以<b>随便挑一个</b>，哪个方便用哪个，失败了就换另一个试。
          </div>
          <div class="hint" style="margin-top:12px; padding-top:11px; border-top:1px dashed var(--line)">
            <b>顶栏「授权 剩余 N 天」指的是什么？</b>
            它是<b>这条 Authorization 自身</b>的剩余有效期，由云盘服务端写死在令牌里（约 30 天），
            <b>不是</b>会员到期日、也不是本地估算 —— 每天自然递减，不用管它。
            剩余不足 <b>15 天</b>时应用每 6 小时会自动续期一次，成功后回到约 30 天（见上方「上次自动续期」）；
            任何请求遇到鉴权失败也会立刻续期并重试一次。
            只有续期本身失败（例如网页端已退出登录、改过密码）时才需要重新抓一条 Authorization 粘进来，
            或者重新扫一次码。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 云盘浏览 ===== -->
    <section class="panel" id="tab-browse">
      <div class="card">
        <div class="hd">
          <h2>云盘浏览</h2>
          <span class="sub">直接操作云端（这里不是挂载点）；删除进云盘回收站，可还原</span>
          <span class="grow"></span>
          <button id="btnUp" class="sm">返回上级</button>
        </div>
        <div class="bd">
          <div class="row" style="margin-bottom:11px">
            <span class="crumb">当前目录：<span id="curDir">/</span></span>
            <span class="grow"></span>
            <button id="btnRemoteMkdir" class="sm">新建文件夹</button>
            <button id="btnRemoteUpload" class="sm">上传文件</button>
            <input id="f_upload" type="file" multiple style="display:none" />
          </div>
          <div class="scroll">
            <table>
              <thead><tr><th>名称</th><th>类型</th><th style="text-align:right">大小</th><th style="text-align:right">操作</th></tr></thead>
              <tbody id="browseBody"><tr><td colspan="4" class="empty">尚未加载</td></tr></tbody>
            </table>
          </div>
          <div class="hint" style="margin-top:10px">
            这里的改动作用在<b>云端</b>：飞牛挂载目录里最多 <b>60 秒</b>后同步（目录缓存 TTL）。
            删除是「移到云盘回收站」，可在 139 网页版或 App 里还原；大文件上传按分片进行，关掉页面会中断。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 远程挂载（WebDAV） ===== -->
    <section class="panel" id="tab-dav">
      <div class="card">
        <div class="hd">
          <h2>远程挂载（WebDAV）</h2><span class="tag" id="davState">未启用</span>
          <span class="tag" id="davWho">—</span>
          <span class="sub">让飞牛「远程挂载」直接挂这块云盘；<b>总开关是全局的</b>，路径 / 用户名 / 密码 / 只读<b>按账号各一套</b></span>
        </div>
        <div class="bd">
          <div class="checkline" style="height:34px">
            <input id="davEnabled" type="checkbox" />
            <span>启用 WebDAV 服务（地址 <code class="k">/dav/</code>，账号密码独立于登录授权）</span>
          </div>
          <div class="grid" style="margin-top:12px">
            <div><label>用户名</label><input id="davUser" spellcheck="false" /></div>
            <div>
              <label>密码</label>
              <div class="row nowrap">
                <input id="davPass" spellcheck="false" />
                <button id="btnDavReset" style="flex:0 0 auto">重置</button>
              </div>
            </div>
          </div>
          <label style="margin-top:12px">连接地址</label>
          <div class="row nowrap">
            <input id="davURL" spellcheck="false" readonly />
            <button id="btnDavCopy" style="flex:0 0 auto">复制地址</button>
            <button id="btnDavCopyAuth" style="flex:0 0 auto">复制账号密码</button>
          </div>
          <div id="davURLs" class="hint" style="margin-top:7px"></div>
          <div class="checkline" style="height:34px">
            <input id="davRedir" type="checkbox" />
            <span>下载直接 302 到云盘 CDN（推荐：不占 NAS 带宽，4K 拖动进度条正常）</span>
          </div>
          <div class="actions">
            <button id="btnDavSave" class="primary">保存</button>
          </div>
          <div id="msgDav" class="msg"></div>

          <!-- 多账号：WebDAV 的路径与凭据各一套，这里给一张总览（单账号时隐藏） -->
          <div id="davAllWrap" style="display:none;margin-top:20px;padding-top:14px;border-top:1px solid rgba(127,127,127,.18)">
            <div class="hd" style="margin-bottom:8px">
              <h2 style="font-size:15px">全部账号的 WebDAV</h2>
              <span class="sub">总开关是全局的；下面每台的地址、用户名、密码、只读各管各的</span>
            </div>
            <div class="scroll" style="max-height:none">
              <table>
                <thead><tr><th>账号</th><th>地址</th><th>用户名</th><th>密码</th><th>读写</th><th>下载</th><th style="text-align:right">操作</th></tr></thead>
                <tbody id="davAllBody"><tr><td colspan="7" class="empty">加载中…</td></tr></tbody>
              </table>
            </div>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="hd"><h2>在飞牛里怎么填</h2><span class="sub">文件 → 远程挂载 → 挂载远程文件夹 → WebDAV</span></div>
        <div class="bd">
          <ol class="steps">
            <li>打开飞牛「文件」，左侧点<b>远程挂载</b>，右上角<b>挂载远程文件夹</b>。</li>
            <li>协议选 <b>WebDAV</b>，地址填上面那条（形如 <code class="k">http://192.168.x.x:19898/dav/</code>）。</li>
            <li>用户名、密码填上面的，远端路径留空（就是云盘根目录），保存。</li>
            <li>飞牛会用 rclone 把它挂成一个卷，出现在「远程挂载」列表里，可以直接拿来建影视库。</li>
          </ol>
          <div class="hint">
            两种挂载方式的取舍：<b>「我的文件」里的挂载</b>是本应用直接挂的 FUSE，
            支持大文件局部写、省流量；<b>远程挂载（WebDAV）</b> 走飞牛自己的 rclone，
            改文件要整文件重传，但胜在是飞牛原生入口、能直接喂给影视/相册这类应用。
            同一个云盘目录别两边同时写：两边缓存各自独立，会互相看不见。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 影视 STRM ===== -->
    <section class="panel" id="tab-strm">
      <div class="card">
        <div class="hd">
          <h2>影视 STRM</h2><span class="tag" id="strmState">未启用</span>
          <span class="tag" id="strmWho">—</span>
          <span class="sub">把云盘里的影视文件生成 .strm，给飞牛影视 / Emby 当索引用；开关与映射<b>只作用于这个账号</b></span>
        </div>
        <div class="bd">
          <div class="checkline" style="height:34px">
            <input id="strmEnabled" type="checkbox" />
            <span>启用：按下面的映射定时扫描云端并生成 .strm</span>
          </div>

          <label style="margin-top:12px">映射（云端目录 → 本地输出目录）</label>
          <div id="strmPairs"></div>
          <div class="actions" style="margin-top:4px">
            <button id="btnStrmAdd" class="sm">+ 添加一条映射</button>
          </div>

          <div class="grid" style="margin-top:14px">
            <div>
              <label>内容形式</label>
              <select id="strmMode">
                <option value="auto">自动（挂载在线写本地路径，否则写直链）</option>
                <option value="path">本地路径（媒体库直接读挂载里的文件）</option>
                <option value="url">直链（http，不依赖挂载）</option>
              </select>
            </div>
            <div><label>扫描间隔（分钟）</label><input id="strmInterval" type="number" min="1" max="1440" /></div>
            <div><label>视频扩展名（逗号分隔，留空用内置）</label><input id="strmExt" placeholder="mp4,mkv,avi,ts,m2ts,iso,wmv,flv,mov,rmvb,m4v,webm" /></div>
            <div>
              <label>云端删掉的文件也删本地 .strm</label>
              <div class="checkline" style="height:38px"><input id="strmDel" type="checkbox" /> <span>关掉则只增不删</span></div>
            </div>
          </div>

          <div class="actions">
            <button id="btnStrmSave" class="primary">保存</button>
            <button id="btnStrmScan">立即扫描</button>
          </div>
          <div id="msgStrm" class="msg"></div>

          <!-- 多账号：每台账号的 STRM 分开管理，这里给一张总览（单账号时自动隐藏） -->
          <div id="strmAllWrap" style="display:none;margin-top:20px;padding-top:14px;border-top:1px solid rgba(127,127,127,.18)">
            <div class="hd" style="margin-bottom:8px">
              <h2 style="font-size:15px">全部账号的 STRM</h2>
              <span class="sub">每台账号各有自己的开关、映射与定时；这里只做总览与快捷操作</span>
            </div>
            <div class="scroll" style="max-height:none">
              <table>
                <thead><tr><th>账号</th><th>开关</th><th>模式</th><th>映射</th><th>.strm</th><th>上次扫描</th><th style="text-align:right">操作</th></tr></thead>
                <tbody id="strmAllBody"><tr><td colspan="7" class="empty">加载中…</td></tr></tbody>
              </table>
            </div>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="hd"><h2>扫描结果</h2><span class="sub" id="strmMeta">还没有扫过</span></div>
        <div class="bd"><div id="strmResults" class="hint">保存配置后点「立即扫描」。</div></div>
      </div>

      <div class="card">
        <div class="hd"><h2>怎么接影视 / Emby</h2><span class="sub">生成的 .strm 就是普通文件，媒体库按目录扫即可</span></div>
        <div class="bd">
          <ol class="steps">
            <li>挑一个<b>不在云盘挂载点里</b>的目录当输出，例如 <code class="k">/vol1/media/strm</code>（不能在挂载点里，否则会写到云端）。</li>
            <li>映射填：云端 <code class="k">/01影视</code> → 本地 <code class="k">/vol1/media/strm/01影视</code>，保存。</li>
            <li>飞牛影视：媒体库 → 添加 → 选电影/电视剧 → 目录选这个本地目录 → 扫描。</li>
            <li>Emby：添加媒体库 → 选内容类型 → 文件夹选该目录 → 扫描媒体库。</li>
          </ol>
          <div class="hint">
            <b>path 模式</b>写的是挂载点里的真实路径：播放走 FUSE，必须挂着，但省流量。<br>
            <b>url 模式</b>写 http 直链（带口令）：播放时 302 跳到云盘 CDN，不依赖挂载、拖进度条正常，媒体服务要能访问本机这个端口。<br>
            云端改名/移动不会留旧条目（下一轮会把旧 .strm 换掉）；云端删掉的文件默认也会一起收走。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 账号（多账号管理） ===== -->
    <section class="panel" id="tab-accounts">
      <div class="card">
        <div class="hd">
          <h2>账号</h2><span class="tag" id="acctCount">—</span>
          <span class="sub">一个控制台管多台 139 云盘：各自授权、各自挂载点、各自 WebDAV 与 STRM</span>
        </div>
        <div class="bd">
          <div class="scroll" style="max-height:none">
            <table>
              <thead><tr><th>账号</th><th>挂载位置</th><th>WebDAV 路径</th><th>挂载状态</th><th style="text-align:right">操作</th></tr></thead>
              <tbody id="acctBody"><tr><td colspan="5" class="empty">尚未加载</td></tr></tbody>
            </table>
          </div>
          <div class="grid" style="margin-top:14px">
            <div><label>备注名（可空，留空就显示账号尾号）</label><input id="newAcctName" spellcheck="false" placeholder="例如 家庭号" /></div>
            <div><label>挂载位置（绝对路径，必须与其它账号互不包含）</label><input id="newAcctMount" spellcheck="false" placeholder="/vol4/1000/移动云盘-2" /></div>
          </div>
          <div class="actions">
            <button id="btnAcctAdd" class="primary">新增账号</button>
            <button id="btnAcctRemountAll" class="ghost">全部重挂</button>
            <button id="btnAcctRefresh" class="ghost">刷新列表</button>
          </div>
          <div class="hint" style="margin-top:10px">
            每行四个按钮：<code class="k">导出</code> 下载该账号配置（<b>不含</b>登录凭据）、
            <code class="k">含凭据</code> 连 <code class="k">authorization</code> 与 <code class="k">dav_password</code> 一起导（明文，别外发）、
            <code class="k">日志</code> 只看这台账号的日志、<code class="k">扫描</code> 立刻跑一轮 STRM（仅开了 STRM 的账号）。
          </div>
          <div class="msg" id="acctMsg"></div>
          <div class="hint" style="margin-top:10px">
            新账号加完要单独授权：切到该账号 → 在「授权」页扫码或粘贴一条它自己的 Authorization → 回「挂载设置」点挂载。<br>
            挂载点之间不能互相包含（<code class="k">/vol4/1000/移动云盘</code> 和 <code class="k">/vol4/1000/移动云盘/电影</code> 只能留一个），
            否则同一棵目录树会被两个账号同时接管。<br>
            WebDAV 路径：默认账号沿用老地址 <code class="k">/dav/</code>，其余账号是 <code class="k">/dav/&lt;账号 ID&gt;/</code>，凭据各自独立。<br>
            「导出」下载的是该账号单独的一份 JSON（迁移/备份/贴工单用），不含 <code class="k">authorization</code> 与
            <code class="k">dav_password</code>；要连凭据一起备份就点同一行的「含凭据」。
            「全部重挂」会逐台重新登录再重新挂载，换完密码或令牌用它。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 日志 ===== -->
    <section class="panel" id="tab-logs">
      <div class="card">
        <div class="hd"><h2>运行日志</h2><span class="sub" id="logSub">最近 200 条</span></div>
        <div class="bd">
          <pre id="logs">加载中…</pre>
          <div class="actions">
            <button id="btnLogs" class="sm">刷新日志</button>
            <div class="checkline"><input id="f_follow" type="checkbox" checked /> <span class="hint">自动刷新并滚到最新</span></div>
            <div class="checkline" id="logAcctWrap" style="display:none"><input id="f_logAcct" type="checkbox" checked />
              <span class="hint">只看当前账号（账号页每行的「日志」按钮会把这里切成那一台）</span></div>
          </div>
        </div>
      </div>
    </section>

  </div>

  <footer class="pagefoot">
    <span>移动云盘 for 飞牛 fnOS · <b>v<span id="footVer">—</span></b></span>
    <span>开发者 <b>vincent</b></span>
    <span class="grow"></span>
    <span>开源 <a href="https://github.com/vincent-jia253/fnos-cmccyun" target="_blank" rel="noreferrer">github.com/vincent-jia253/fnos-cmccyun</a> · MIT</span>
  </footer>
</main>

<!-- ===== 位置选择器 ===== -->
<div id="pickModal" class="modal" style="display:none">
  <div class="box">
    <h3>选择挂载位置</h3>
    <div class="sub">左边文件夹树（点 <b>▸</b> 展开、点名字选中），右边是选中目录里的内容（点一行就进去）。挂进个人空间（<code class="k">/volN/&lt;uid&gt;</code>）后，飞牛「文件」里就能直接看到。</div>
    <div class="row" id="pickLinks" style="margin-bottom:11px"></div>
    <div class="pickwrap">
      <div class="picktree">
        <div class="picker-head">存储空间</div>
        <div id="pickTree"></div>
      </div>
      <div class="picklist">
        <div class="picker-head" id="pickCur">
          <span id="pickPath">/</span>
          <button id="pickReload" class="qlink" style="float:right;margin-top:-3px">↻ 刷新</button>
        </div>
        <div id="pickBody"></div>
      </div>
    </div>
    <div class="row" style="margin-top:11px">
      <input id="pickNewName" placeholder="新建文件夹名（例如 移动云盘）" style="max-width:240px; flex:0 1 auto" />
      <button id="pickMkdir" class="sm">在选中目录里新建文件夹</button>
      <div class="checkline"><input id="pickShowSys" type="checkbox" /> <span class="hint">显示系统目录</span></div>
    </div>
    <div class="row" style="margin-top:9px">
      <div class="checkline"><input id="pickCreate" type="checkbox" checked /> <span class="hint">目录不存在时自动创建</span></div>
      <div class="checkline"><input id="pickForce" type="checkbox" /> <span class="hint">我确认会遮住目标目录里原有的内容</span></div>
    </div>
    <div class="row nowrap" style="margin-top:11px">
      <label style="margin:0; flex:0 0 auto">路径</label>
      <input id="pickPathInput" placeholder="/vol4/1000/移动云盘" spellcheck="false" />
      <button id="pickGo" style="flex:0 0 auto">跳转</button>
    </div>
    <div id="pickMsg" class="msg"></div>
    <div class="foot">
      <span class="sel" id="pickSel">还没选目录</span>
      <button id="pickApply" class="primary">挂到这里</button>
      <button id="pickClose">取消</button>
    </div>
  </div>
</div>

<div id="cloudPickModal" class="modal" style="display:none">
  <div class="box" style="width:640px;max-width:96vw">
    <h3>选择云盘目录</h3>
    <div class="sub">点目录进去逐级浏览；当前所在目录就是映射里的「云端目录」。根目录表示整盘。</div>
    <div class="picker-head" id="cloudPickCur"><span id="cloudPickPath">/</span></div>
    <div id="cloudPickList" style="max-height:44vh;overflow:auto;border:1px solid rgba(127,127,127,.22);border-radius:10px;padding:4px"></div>
    <div class="foot">
      <button id="cloudPickUp" class="sm ghost">上一级</button>
      <button id="cloudPickRoot" class="sm ghost">根目录</button>
      <span class="sel" id="cloudPickMsg"></span>
      <button id="cloudPickOK" class="primary" style="margin-left:auto">选这个目录</button>
      <button id="cloudPickCancel" class="sm ghost">取消</button>
    </div>
  </div>
</div>

<div id="toasts"></div>

<script>
"use strict";
var $ = function (id) { return document.getElementById(id); };
var cfg = {};
var curDir = "/";
var dirStack = [];
var mounted = false;
var lastSt = null;      // 最近一次成功读取的状态
var browseLoaded = false;
var curAccount = "";    // 当前操作的账号 ID；"" 表示默认账号
var logAccount = "";    // 日志页要过滤的账号 ID；账号页每行「日志」按钮会设它
var accounts = [];      // /api/status 里的 accounts（不含密文）
var curTab = "mount";

/* ---------------- 消息：toast + 行内 ---------------- */
function toast(text, kind) {
  var box = $("toasts");
  var n = document.createElement("div");
  n.className = "toast " + (kind || "");
  var mk = document.createElement("i"); mk.className = "mk";
  var tx = document.createElement("div"); tx.className = "tx"; tx.textContent = text || "";
  var x = document.createElement("button"); x.className = "x"; x.textContent = "×";
  x.onclick = function () { n.remove(); };
  n.appendChild(mk); n.appendChild(tx); n.appendChild(x);
  box.appendChild(n);
  if (kind !== "err") setTimeout(function () { n.remove(); }, kind === "warn" ? 7000 : 4000);
  while (box.children.length > 4) box.removeChild(box.firstChild);
}

function say(el, text, ok) {
  var n = $(el);
  if (!n) return;
  n.textContent = text || "";
  n.className = "msg " + (ok === undefined ? "" : (ok ? "ok" : "err"));
}

/* 所有 /api 请求都带上当前账号：不带参数时后端按默认账号处理，
   所以单账号用户看到的行为和以前一模一样。 */
function withAcct(path) {
  if (path.indexOf("/api/") !== 0) return path;
  if (path.indexOf("account=") >= 0) return path;
  if (!curAccount) return path;
  return path + (path.indexOf("?") >= 0 ? "&" : "?") + "account=" + encodeURIComponent(curAccount);
}

/* ---------------- 请求（带超时，绝不无限转圈） ---------------- */
async function api(path, opts) {
  opts = opts || {};
  var ms = opts.timeout || 25000;
  var ac = new AbortController();
  var timer = setTimeout(function () { ac.abort(); }, ms);
  try {
    var req = Object.assign({ headers: { "Content-Type": "application/json" }, signal: ac.signal }, opts);
    var res = await fetch(withAcct(path), req);
    var data = await res.json().catch(function () { return { ok: false, error: "响应解析失败（HTTP " + res.status + "）" }; });
    if (!res.ok || data.ok === false) throw new Error(data.error || ("HTTP " + res.status));
    return data;
  } catch (e) {
    if (e && (e.name === "AbortError" || String(e).indexOf("aborted") >= 0)) {
      throw new Error("请求超时（" + Math.round(ms / 1000) + " 秒无响应）：服务正在忙或卡住了。可以点右上角 ↻ 重新读取状态，或看「运行日志」");
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

function busy(btn, on, text) {
  if (!btn) return;
  if (on) {
    if (!btn.dataset.old) btn.dataset.old = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '<i class="sp"></i>' + (text || "处理中…");
  } else {
    btn.disabled = false;
    if (btn.dataset.old) { btn.innerHTML = btn.dataset.old; delete btn.dataset.old; }
  }
}

function fmtSize(n) {
  if (!n) return "0 B";
  var u = ["B", "KB", "MB", "GB", "TB"], i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(i ? 1 : 0) + " " + u[i];
}

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, function (c) {
    return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
  });
}

/* 授权天数的悬停说明：这是「这条 Authorization 自身」的剩余有效期，不是会员到期日。 */
var AUTH_TIP = [
  "这是当前这条 Authorization 自身的剩余有效期（扫码登录和手工粘贴拿到的完全一样），由云盘服务端写死在令牌里，约 30 天。",
  "不是会员到期日，也不是本地估算。",
  "",
  "· 只是个倒计时，每天自然递减，无需理会；",
  "· 剩余不足 15 天时，应用每 6 小时会自动续期一次，成功后回到约 30 天；",
  "· 任何请求遇到鉴权失败，也会立刻强制续期并重试一次；",
  "· 只有续期本身失败（例如网页端已退出登录、改过密码）时，才需要重扫二维码或重新抓一条 Authorization 粘进来。",
  "",
  "也可以点「刷新令牌」立即续期一次（可用来确认续期可用）。"
].join("\n");

/* ---------------- 标签页 ---------------- */
function showTab(name) {
  curTab = name;
  var tabs = document.querySelectorAll(".tab");
  for (var i = 0; i < tabs.length; i++) {
    tabs[i].classList.toggle("act", tabs[i].dataset.tab === name);
  }
  var panels = document.querySelectorAll(".panel");
  for (var j = 0; j < panels.length; j++) {
    panels[j].classList.toggle("act", panels[j].id === "tab-" + name);
  }
  if (name === "accounts") renderAcctTable();
  if (name === "logs") loadLogs();
  if (name === "dav") loadDav();
  if (name === "strm") loadStrm();
  if (name === "browse" && !browseLoaded && lastSt && lastSt.has_token) { browseLoaded = true; browse("/"); }
  // 第一次进「授权」页又没授权时，直接把二维码摆出来——少点一次按钮
  if (name === "auth" && !qrAutoStarted && lastSt && !lastSt.has_token) { qrAutoStarted = true; qrStart(); }
}
document.querySelectorAll(".tab").forEach(function (b) {
  b.onclick = function () { showTab(b.dataset.tab); };
});

/* ---------------- 控制台访问密码 ---------------- */
(function () {
  var btn = $("btnWebPass");
  if (btn) btn.onclick = async function () {
    var cur = $("webPassCur").value, nw = $("webPassNew").value, nw2 = $("webPassNew2").value;
    var user = $("webUser").value.trim();
    if (!cur) { say("msgWebPass", "先填当前密码", false); $("webPassCur").focus(); return; }
    if (nw && nw !== nw2) { say("msgWebPass", "两次输入的新密码不一致", false); return; }
    if (nw && nw.length < 4) { say("msgWebPass", "新密码至少 4 位", false); return; }
    if (!nw && !user) { say("msgWebPass", "新密码和登录名至少改一个", false); return; }
    busy(btn, true, "保存中…");
    try {
      var d = await api("/api/console-password", { method: "POST", body: JSON.stringify({ current: cur, password: nw, confirm: nw2, user: user }) });
      say("msgWebPass", d.message || "已保存", true);
      $("webPassCur").value = ""; $("webPassNew").value = ""; $("webPassNew2").value = "";
      toast("控制台密码已更新", "ok");
      await refresh();
    } catch (e) { say("msgWebPass", e.message, false); }
    finally { busy(btn, false); }
  };
  var lo = $("btnLogout");
  if (lo) lo.onclick = async function () {
    if (!confirm("退出登录？之后打开控制台要重新输密码。")) return;
    try { await api("/api/logout", { method: "POST" }); } catch (e) {}
    window.location = "/login";
  };
})();

/* 顶栏 / 设置里提示「还在用默认密码」 */
var webPassSet = true;
function refreshConsoleAuthHint() {
  var chip = $("webPassDefault");
  if (!chip) return;
  chip.style.display = webPassSet ? "none" : "";
}

/* ---------------- 账号（多账号切换 / 管理） ---------------- */
function acctExists(id) {
  for (var i = 0; i < accounts.length; i++) if (accounts[i].id === id) return true;
  return false;
}

function curAcctObj() {
  for (var i = 0; i < accounts.length; i++) if (accounts[i].id === curAccount) return accounts[i];
  for (var j = 0; j < accounts.length; j++) if (accounts[j].default) return accounts[j];
  return null;
}

/* 顶栏切换器：账号只有一个时不显示，保持单账号界面干净 */
function renderAcctSel() {
  var sel = $("acctSel");
  if (!sel) return;
  var sig = "";
  for (var i = 0; i < accounts.length; i++) sig += accounts[i].id + "|" + (accounts[i].name || "") + ";";
  if (sel.dataset.sig !== sig) {
    sel.dataset.sig = sig;
    var html = "";
    for (var j = 0; j < accounts.length; j++) {
      var a = accounts[j];
      var lab = a.name || a.id;
      if (a.default) lab += "（默认）";
      if (!a.authorization_set) lab += " · 未授权";
      html += '<option value="' + esc(a.id) + '">' + esc(lab) + "</option>";
    }
    sel.innerHTML = html;
  }
  if (curAccount) sel.value = curAccount;
  sel.style.display = accounts.length > 1 ? "" : "none";
  sel.title = "切换账号：下面每个页签都只作用在当前账号上（共 " + accounts.length + " 个）";
}

function acctBtns(a, isCur) {
  var b = [];
  if (!isCur) b.push('<button class="sm" data-act="switch" data-id="' + esc(a.id) + '">切换</button>');
  if (!a.default) b.push('<button class="sm ghost" data-act="default" data-id="' + esc(a.id) + '">设为默认</button>');
  b.push('<button class="sm ghost" data-act="rename" data-id="' + esc(a.id) + '">改名</button>');
  b.push('<button class="sm ghost" data-act="export" data-id="' + esc(a.id) + '" title="下载这个账号的配置（不含登录凭据）">导出</button>');
  b.push('<button class="sm ghost" data-act="export_auth" data-id="' + esc(a.id) + '" title="下载这个账号的配置，含 authorization 与 dav_password（明文！）">含凭据</button>');
  b.push('<button class="sm ghost" data-act="logs" data-id="' + esc(a.id) + '" title="只看这个账号的日志">日志</button>');
  if (a.strm_enabled) b.push('<button class="sm ghost" data-act="scan" data-id="' + esc(a.id) + '" title="立刻对这台账号跑一轮 STRM 扫描">扫描</button>');
  if (accounts.length > 1) b.push('<button class="sm danger" data-act="delete" data-id="' + esc(a.id) + '">删除</button>');
  return b.join(" ");
}

function renderAcctTable() {
  var body = $("acctBody");
  if (!body) return;
  var cnt = $("acctCount");
  if (!accounts.length) {
    body.innerHTML = '<tr><td colspan="5" class="empty">还没有账号</td></tr>';
    if (cnt) cnt.textContent = "0 个";
    return;
  }
  if (cnt) cnt.textContent = accounts.length + " 个";
  var html = "";
  for (var i = 0; i < accounts.length; i++) {
    var a = accounts[i];
    var isCur = (a.id === curAccount) || (!curAccount && a.default);
    var tags = [];
    if (a.default) tags.push('<span class="tag">默认</span>');
    if (isCur) tags.push('<span class="tag">当前</span>');
    if (a.mounted) tags.push('<span class="chip ok" style="padding:2px 8px">已挂载</span>');
    if (!a.authorization_set) tags.push('<span class="chip warn" style="padding:2px 8px">未授权</span>');
    if (a.strm_enabled) tags.push('<span class="chip" style="padding:2px 8px">STRM</span>');
    var mp = a.mount_point
      ? '<code class="k">' + esc(a.mount_point) + "</code>"
      : '<span class="hint">未设置</span>';
    if (a.read_only) mp += ' <span class="hint">只读</span>';
    var st = a.mounted ? "已挂载" : "未挂载";
    if (a.dead_mount) st = "残留挂载（失联）";
    if (a.last_error) st += '<div class="hint" style="color:var(--err)">' + esc(a.last_error) + "</div>";
    html += "<tr>" +
      '<td><div class="fname"><span><b>' + esc(a.name || a.id) + "</b>" +
        (a.account ? ' <span class="hint">' + esc(a.account) + "</span>" : "") +
      '</span></div><div class="hint" style="margin-top:3px">' + tags.join(" ") + "</div></td>" +
      "<td>" + mp + "</td>" +
      '<td><code class="k">' + esc(a.dav_path || "") + "</code>" +
        (a.dav_password_set ? "" : ' <span class="hint">未设密码</span>') + "</td>" +
      "<td>" + st + "</td>" +
      '<td style="text-align:right;white-space:nowrap">' + acctBtns(a, isCur) + "</td></tr>";
  }
  body.innerHTML = html;
}

/* 切换账号后，当前页签的内容必须跟着换（否则会看着旧账号的数据发呆） */
function reloadTabForAccount() {
  browseLoaded = false;
  curDir = "/";
  dirStack = [];
  if (curTab === "dav") loadDav();
  if (curTab === "strm") loadStrm();
  if (curTab === "accounts") renderAcctTable();
  if (curTab === "browse" && lastSt && lastSt.has_token) { browseLoaded = true; browse("/"); }
  loadShares();
}

async function selectAccount(id) {
  if (!id || id === curAccount) return;
  curAccount = id;
  renderAcctSel();
  renderAcctTable();
  try { await refresh(); } catch (e) { /* refresh 自己会报错 */ }
  reloadTabForAccount();
  var a = curAcctObj();
  toast("已切换到「" + ((a && a.name) || id) + "」", "ok");
}

async function acctPost(body, btn, msg, okText) {
  if (msg) say("acctMsg", "", true);
  busy(btn, true, "处理中…");
  try {
    var tmo = body.action === "remount_all" ? 600000 : 60000;
    var d = await api("/api/accounts", { method: "POST", body: JSON.stringify(body), timeout: tmo });
    if (body.action === "add" && d.id) {
      curAccount = d.id;
      accounts = d.accounts || accounts;
      toast(okText || "账号已新增：去「授权」页给它授权", "ok");
      await refresh();
      showTab("auth");
    } else {
      accounts = d.accounts || accounts;
      if (body.action === "delete" && !acctExists(curAccount)) {
        curAccount = d.default || (accounts[0] && accounts[0].id) || "";
      }
      toast(okText || "已保存", "ok");
      await refresh();
      reloadTabForAccount();
    }
    return d;
  } catch (e) {
    if (msg) say("acctMsg", e.message, false);
    toast(e.message, "err");
    return null;
  } finally {
    busy(btn, false);
  }
}

(function bindAccounts() {
  var sel = $("acctSel");
  if (sel) sel.onchange = function () { selectAccount(this.value); };
  var add = $("btnAcctAdd");
  if (add) add.onclick = async function () {
    var name = $("newAcctName").value.trim();
    var mp = $("newAcctMount").value.trim();
    if (!mp) { say("acctMsg", "先填挂载位置（绝对路径，例如 /vol4/1000/移动云盘-2）", false); $("newAcctMount").focus(); return; }
    if (mp.charAt(0) !== "/") { say("acctMsg", "挂载位置要写绝对路径，以 / 开头", false); return; }
    var d = await acctPost({ action: "add", name: name, mount_point: mp }, this, "acctMsg");
    if (d) { $("newAcctName").value = ""; $("newAcctMount").value = ""; }
  };
  var rf = $("btnAcctRefresh");
  if (rf) rf.onclick = async function () { await refresh(); renderAcctTable(); toast("账号列表已刷新", "ok"); };
  /* 全部重挂：换完密码/令牌以后不用一台台点，逐台重登再重挂，失败的单独报 */
  var rma = $("btnAcctRemountAll");
  if (rma) rma.onclick = async function () {
    if (!confirm("依次重挂全部账号？\n\n每个账号都会重新登录云端再重新挂载，期间它的目录读写会短暂失败（通常几秒到十几秒）。\n失败的那台会单独报错，不影响其它账号。")) return;
    var d = await acctPost({ action: "remount_all" }, this, "acctMsg");
    if (!d) return;
    var rs = d.results || [], okN = 0, bad = [];
    for (var i = 0; i < rs.length; i++) {
      if (rs[i].ok) okN++;
      else bad.push((rs[i].name || rs[i].id) + "：" + (rs[i].error || "失败"));
    }
    say("acctMsg", "已重挂 " + okN + "/" + rs.length + " 台" + (bad.length ? "；失败：" + bad.join("；") : ""), bad.length === 0);
  };
  var body = $("acctBody");
  if (body) body.onclick = async function (ev) {
    var t = ev.target;
    while (t && t !== body && !(t.tagName === "BUTTON" && t.dataset && t.dataset.act)) t = t.parentNode;
    if (!t || t === body) return;
    var act = t.dataset.act, id = t.dataset.id;
    if (act === "switch") { await selectAccount(id); return; }
    if (act === "export") {
      window.location = "/api/accounts/export?id=" + encodeURIComponent(id);
      say("acctMsg", "已开始下载该账号的配置（不含登录凭据）", true);
      return;
    }
    if (act === "export_auth") {
      if (!confirm("导出「含凭据」的配置？\n\n文件里会带 authorization（云盘登录态）和 dav_password（WebDAV 密码）的明文。\n拿到这份文件就等于拿到这台云盘——别放进聊天、工单或公开仓库。")) return;
      window.location = "/api/accounts/export?id=" + encodeURIComponent(id) + "&include_auth=1";
      say("acctMsg", "已开始下载该账号的配置（含登录凭据，用完请删掉）", true);
      return;
    }
    if (act === "logs") {
      logAccount = id;
      showTab("logs");
      loadLogs();
      return;
    }
    if (act === "scan") {
      busy(t, true, "扫描中…");
      try {
        var d = await api("/api/strm/scan?account=" + encodeURIComponent(id), { method: "POST", timeout: 900000 });
        var n = 0, upd = 0, sk = 0;
        var rs = d.results || [];
        for (var k = 0; k < rs.length; k++) { n += rs[k].total || 0; upd += rs[k].updated || 0; sk += rs[k].skipped || 0; }
        if (d.ok) toast("STRM 扫描完成：共 " + n + " 个（更新 " + upd + "，未变 " + sk + "）", "ok");
        else toast("STRM 扫描失败：" + (d.error || "未知错误"), "err");
      } finally { busy(t, false); }
      return;
    }
    if (act === "default") { await acctPost({ action: "default", id: id }, t, "acctMsg", "已设为默认账号（老地址 /dav/ 也跟着变）"); return; }
    if (act === "rename") {
      var a = null;
      for (var i = 0; i < accounts.length; i++) if (accounts[i].id === id) a = accounts[i];
      var n = prompt("新的备注名（留空则显示账号尾号）", (a && a.custom_name) || "");
      if (n === null) return;
      await acctPost({ action: "update", id: id, name: n }, t, "acctMsg", "备注名已改");
      return;
    }
    if (act === "delete") {
      var who = id, isDef = false;
      for (var j = 0; j < accounts.length; j++) {
        if (accounts[j].id === id) { who = accounts[j].name || id; isDef = !!accounts[j].default; }
      }
      var warn = isDef ? "\n\n这是默认账号：删掉之后 /dav/ 会交给剩下的那台，控制台默认打开的也是它。" : "";
      if (!confirm("删除账号「" + who + "」？" + warn + "\n\n会先卸载它的挂载点，再删掉本地配置（云端文件和已生成的 .strm 不受影响）。")) return;
      await acctPost({ action: "delete", id: id }, t, "acctMsg", "账号已删除");
      return;
    }
  };
})();

/* ---------------- 影视 STRM ---------------- */
var strmData = null;

/* ---------------- 云盘目录选择器（STRM 映射用） ----------------
   云盘接口只认 fileId，所以这里靠 /api/remote/list 逐级下钻，
   靠 /api/remote/resolve 把已有映射的路径还原成「目录链」，
   选完把 "/a/b/c" 写回映射的云端目录。 */
var cloudPickCb = null, cloudPickChain = [], cloudPickID = "";

function cloudPickMsg(text) { var n = $("cloudPickMsg"); if (n) n.textContent = text || ""; }

function cloudPickPathText() {
  var segs = cloudPickChain.map(function (x) { return x.name; });
  return segs.length ? "/" + segs.join("/") : "/";
}

async function cloudPickLoad(id, chain) {
  cloudPickID = id || "";
  cloudPickChain = chain || [];
  $("cloudPickPath").textContent = cloudPickPathText();
  var box = $("cloudPickList");
  box.innerHTML = '<div class="hint" style="padding:10px">加载中…</div>';
  try {
    var d = await api("/api/remote/list?dir=" + encodeURIComponent(cloudPickID), { timeout: 60000 });
    var dirs = (d.entries || []).filter(function (e) { return e.is_dir; });
    if (!dirs.length) {
      box.innerHTML = '<div class="hint" style="padding:10px">（这里没有子目录，可以直接点「选这个目录」）</div>';
      return;
    }
    box.innerHTML = dirs.map(function (e) {
      return '<div class="qlink" style="display:block;padding:8px 10px;border-radius:8px" data-id="' + esc(e.id) + '" data-name="' + esc(e.name) + '">📁 ' + esc(e.name) + "</div>";
    }).join("");
  } catch (e) {
    box.innerHTML = '<div class="hint" style="padding:10px;color:var(--err)">' + esc(e.message) + "</div>";
  }
}

function openCloudPicker(cb, startPath) {
  cloudPickCb = (typeof cb === "function") ? cb : null;
  cloudPickMsg("");
  $("cloudPickModal").style.display = "flex";
  var start = (startPath || "").trim();
  if (!start || start === "/") { cloudPickLoad("", []); return; }
  api("/api/remote/resolve?path=" + encodeURIComponent(start), { timeout: 60000 }).then(function (d) {
    var chain = d.chain || [];
    cloudPickLoad(d.id || "", chain);
    if (d.found === false) cloudPickMsg("「" + start + "」里有目录不存在（断在 " + (d.missing || "?") + "），已停在最后一级存在的目录");
  }).catch(function (e) {
    cloudPickMsg("按当前值打开失败：" + e.message);
    cloudPickLoad("", []);
  });
}

$("cloudPickList").onclick = function (ev) {
  var row = ev.target.closest("[data-id]");
  if (!row) return;
  var chain = cloudPickChain.slice();
  chain.push({ id: row.dataset.id, name: row.dataset.name });
  cloudPickLoad(row.dataset.id, chain);
};
$("cloudPickUp").onclick = function () {
  if (!cloudPickChain.length) return;
  var chain = cloudPickChain.slice(0, -1);
  var parent = chain.length ? chain[chain.length - 1].id : "";
  cloudPickLoad(parent, chain);
};
$("cloudPickRoot").onclick = function () { cloudPickLoad("", []); };
$("cloudPickCancel").onclick = function () { $("cloudPickModal").style.display = "none"; cloudPickCb = null; };
$("cloudPickOK").onclick = function () {
  var path = cloudPickPathText();
  var cb = cloudPickCb;
  $("cloudPickModal").style.display = "none";
  cloudPickCb = null;
  if (cb) cb(path);
};

function strmRow(p) {
  var d = document.createElement("div");
  d.className = "row nowrap";
  d.style.marginBottom = "8px";
  d.innerHTML =
    '<input class="scloud" spellcheck="false" readonly placeholder="点「选云端…」浏览云盘目录" value="' + esc((p && p.cloud) || "") + '" style="flex:1 1 38%;cursor:pointer" title="点一下选择云盘目录" />' +
    '<button class="sm scpick" style="flex:0 0 auto">选云端…</button>' +
    '<span class="hint" style="flex:0 0 auto;padding:0 2px">→</span>' +
    '<input class="slocal" spellcheck="false" placeholder="/vol1/media/strm" value="' + esc((p && p.local) || "") + '" style="flex:1 1 42%" />' +
    '<button class="sm spick" style="flex:0 0 auto">选本地…</button>' +
    '<button class="sm sdel" style="flex:0 0 auto">删</button>';
  // 云端目录：输入框只读，点它或点「选云端…」都打开云盘目录选择器
  var cloudInput = d.querySelector(".scloud");
  var pickCloud = function () {
    openCloudPicker(function (path) { cloudInput.value = path; });
  };
  cloudInput.onclick = pickCloud;
  d.querySelector(".scpick").onclick = pickCloud;
  d.querySelector(".spick").onclick = function () {
    openPicker(function (path) { d.querySelector(".slocal").value = path; });
  };
  d.querySelector(".sdel").onclick = function () { d.remove(); };
  return d;
}

function strmPairsFromDom() {
  var out = [];
  var rows = document.querySelectorAll("#strmPairs .row");
  for (var i = 0; i < rows.length; i++) {
    var c = rows[i].querySelector(".scloud").value.trim();
    var l = rows[i].querySelector(".slocal").value.trim();
    if (!l) continue;
    out.push({ cloud: c, local: l });
  }
  return out;
}

function strmShow(d) {
  strmData = d || {};
  $("strmEnabled").checked = !!strmData.enabled;
  $("strmMode").value = strmData.mode_setting || "auto";
  $("strmInterval").value = strmData.interval_min || 5;
  $("strmExt").value = strmData.ext || "";
  $("strmDel").checked = !!strmData.delete_missing;
  $("strmState").textContent = strmData.enabled ? "已启用" : "未启用";
  if ($("strmWho")) {
    var who = strmData.account || "";
    for (var wi = 0; wi < accounts.length; wi++) if (accounts[wi].id === who) who = accounts[wi].name || who;
    $("strmWho").textContent = who ? ("账号 " + who) : "账号 —";
  }

  var box = $("strmPairs");
  box.innerHTML = "";
  var list = strmData.pairs || [];
  if (!list.length) { box.appendChild(strmRow(null)); }
  for (var i = 0; i < list.length; i++) box.appendChild(strmRow(list[i]));

  var meta = [];
  if (strmData.last_at) meta.push("上次扫描 " + String(strmData.last_at).slice(0, 19).replace("T", " "));
  meta.push("当前内容形式：" + (strmData.mode === "path" ? "本地路径" : "直链") + (strmData.mode_setting === "auto" ? "（自动判定）" : ""));
  if (strmData.enabled && !strmData.mount_ready && strmData.mode === "url") meta.push("挂载未在线，已自动用直链");
  $("strmMeta").textContent = meta.join(" · ");

  var r = strmData.last;
  var html = "";
  if (r && r.length) {
    for (var j = 0; j < r.length; j++) {
      var x = r[j];
      var line = "<div><b>" + esc(x.cloud || "/") + "</b> → <code class=\"k\">" + esc(x.local) + "</code>：共 " + x.total +
        " 个（新增 " + x.added + "，更新 " + x.updated + "，删除 " + x.removed + "），" + x.duration_ms + " ms";
      if (x.truncated) line += " <b>（条目太多，已截断）</b>";
      if (x.error) line += " <span style=\"color:#d33\">失败：" + esc(x.error) + "</span>";
      if (x.errors && x.errors.length) line += "<div class=\"hint\">" + x.errors.slice(0, 3).map(esc).join("<br>") + "</div>";
      line += "</div>";
      html += line;
    }
  } else {
    html = strmData.enabled ? "还没有扫描记录。" : "启用后保存，再点「立即扫描」。";
  }
  $("strmResults").innerHTML = html;
}

async function loadStrm() {
  try { var d = await api("/api/strm/status"); strmShow(d.strm); }
  catch (e) { say("msgStrm", e.message, false); }
  loadStrmAccounts();
}

/* 全部账号的 STRM 总览：多账号才有意义，单账号直接收起 */
async function loadStrmAccounts() {
  var wrap = $("strmAllWrap"), body = $("strmAllBody");
  if (!wrap || !body) return;
  if (accounts.length < 2) { wrap.style.display = "none"; return; }
  wrap.style.display = "";
  try {
    var d = await api("/api/strm/accounts");
    var rows = d.accounts || [];
    if (!rows.length) { body.innerHTML = '<tr><td colspan="7" class="empty">还没有账号</td></tr>'; return; }
    var html = "";
    for (var i = 0; i < rows.length; i++) {
      var a = rows[i];
      var isCur = (a.id === curAccount) || (!curAccount && a.default);
      var name = esc(a.name || a.id) + (a.default ? ' <span class="tag">默认</span>' : "") + (isCur ? ' <span class="tag">当前</span>' : "");
      var on = a.enabled ? '<span class="chip ok" style="padding:2px 8px">开</span>' : '<span class="chip warn" style="padding:2px 8px">关</span>';
      if (a.enabled && a.running) on = '<span class="chip" style="padding:2px 8px">扫描中</span>';
      var last = a.last_at ? esc(String(a.last_at).replace("T", " ").slice(0, 19)) : '<span class="hint">还没扫过</span>';
      if (a.last_error) last += '<div class="hint" style="color:var(--err)">' + esc(a.last_error) + "</div>";
      var cnt = a.enabled ? (a.strm_count + " 个") : '<span class="hint">—</span>';
      if (a.enabled && a.pairs && !a.exists) cnt += ' <span class="hint">（有输出目录不存在）</span>';
      html += "<tr>" +
        "<td>" + name + '<div class="hint">' + esc(a.account || "") + "</div></td>" +
        "<td>" + on + "</td>" +
        "<td>" + esc(a.mode || "auto") + "</td>" +
        "<td>" + (a.pairs || 0) + " 条</td>" +
        "<td>" + cnt + "</td>" +
        "<td>" + last + "</td>" +
        '<td style="text-align:right;white-space:nowrap">' +
          (isCur ? "" : '<button class="sm ghost" data-strm-act="switch" data-id="' + esc(a.id) + '">切到这台</button> ') +
          (a.enabled ? '<button class="sm ghost" data-strm-act="scan" data-id="' + esc(a.id) + '">立即扫描</button>' : "") +
        "</td></tr>";
    }
    body.innerHTML = html;
  } catch (e) {
    body.innerHTML = '<tr><td colspan="7" class="empty">' + esc(e.message) + "</td></tr>";
  }
}

/* 总览里的快捷操作：切账号 / 对这一台立即扫描（不切换当前账号） */
(function () {
  var body = $("strmAllBody");
  if (!body) return;
  body.onclick = async function (ev) {
    var t = ev.target;
    while (t && t !== body && !(t.tagName === "BUTTON" && t.dataset && t.dataset.strmAct)) t = t.parentNode;
    if (!t || t === body) return;
    var act = t.dataset.strmAct, id = t.dataset.id;
    if (act === "switch") { await selectAccount(id); showTab("strm"); return; }
    if (act === "scan") {
      busy(t, true, "扫描中…");
      try {
        var d = await api("/api/strm/scan?account=" + encodeURIComponent(id), { method: "POST", timeout: 900000 });
        var n = 0;
        (d.results || []).forEach(function (x) { n += x.total || 0; });
        toast(d.ok ? ("扫描完成：共 " + n + " 个视频") : ("扫描失败：" + (d.error || "未知错误")), d.ok ? "ok" : "err");
        loadStrmAccounts();
      } catch (e) { toast(e.message, "err"); }
      finally { busy(t, false); }
    }
  };
})();

async function strmSave() {
  var body = {
    enabled: $("strmEnabled").checked,
    mode: $("strmMode").value,
    interval_min: Number($("strmInterval").value) || 5,
    ext: $("strmExt").value,
    delete_missing: $("strmDel").checked,
    pairs: strmPairsFromDom()
  };
  var b = $("btnStrmSave");
  busy(b, true);
  try {
    var d = await api("/api/strm/config", { method: "POST", body: JSON.stringify(body) });
    strmShow(d.strm);
    say("msgStrm", body.enabled ? "已保存，之后每 " + body.interval_min + " 分钟自动同步一次" : "已保存，定时扫描已关闭", true);
  } catch (e) {
    say("msgStrm", e.message, false);
  } finally { busy(b, false); }
}

async function strmScan() {
  var b = $("btnStrmScan");
  busy(b, true, "扫描中…");
  say("msgStrm", "正在扫描云端目录并写 .strm，目录大的话要几分钟…");
  try {
    var d = await api("/api/strm/scan", { method: "POST", timeout: 900000 });
    strmShow(d.strm);
    var n = 0;
    (d.results || []).forEach(function (x) { n += x.total || 0; });
    say("msgStrm", "扫描完成：共 " + n + " 个视频", true);
  } catch (e) {
    say("msgStrm", e.message, false);
    try { var s2 = await api("/api/strm/status"); strmShow(s2.strm); } catch (e2) {}
  } finally { busy(b, false); }
}

$("btnStrmSave").onclick = strmSave;
$("btnStrmScan").onclick = strmScan;
$("btnStrmAdd").onclick = function () { $("strmPairs").appendChild(strmRow(null)); };

/* ---------------- 远程挂载（WebDAV） ---------------- */
var davData = null;

function davShow(d) {
  davData = d || {};
  $("davEnabled").checked = !!davData.enabled;
  $("davUser").value = davData.user || "";
  $("davPass").value = davData.password || "";
  $("davRedir").checked = davData.redirect !== false;
  var urls = davData.urls || [];
  $("davURL").value = urls[0] || "";
  var extra = "";
  for (var i = 1; i < urls.length; i++) extra += "<div>" + esc(urls[i]) + "</div>";
  $("davURLs").innerHTML = urls.length > 1
    ? "多网卡：首选上面那条，其余是 " + extra
    : (urls.length ? "填这条就行（本机网卡的局域网地址）。" : "还没读到本机 IP。");
  $("davState").textContent = davData.enabled ? "已启用" : "未启用";
  if ($("davWho")) {
    var who = davData.url ? (davData.path || "") : "";
    var uname = "";
    for (var wi = 0; wi < accounts.length; wi++) {
      if (accounts[wi].id === davData.account) uname = accounts[wi].name || accounts[wi].id;
    }
    $("davWho").textContent = uname ? ("账号 " + uname + " · " + (davData.path || "")) : "账号 —";
    void who;
  }
}

async function loadDav() {
  try { var d = await api("/api/dav/info"); davShow(d.dav); }
  catch (e) { say("msgDav", e.message, false); }
  loadDavAccounts();
}

/* 全部账号的 WebDAV 总览：多账号才有意义，单账号收起 */
async function loadDavAccounts() {
  var wrap = $("davAllWrap"), body = $("davAllBody");
  if (!wrap || !body) return;
  if (accounts.length < 2) { wrap.style.display = "none"; return; }
  wrap.style.display = "";
  try {
    var d = await api("/api/dav/accounts");
    var rows = d.accounts || [];
    if (!rows.length) { body.innerHTML = '<tr><td colspan="7" class="empty">还没有账号</td></tr>'; return; }
    var html = "";
    for (var i = 0; i < rows.length; i++) {
      var a = rows[i];
      var isCur = (a.id === curAccount) || (!curAccount && a.default);
      var name = esc(a.name || a.id) + (a.default ? ' <span class="tag">默认</span>' : "") + (isCur ? ' <span class="tag">当前</span>' : "");
      var ro = a.read_only ? '<span class="chip warn" style="padding:2px 8px">只读</span>' : '<span class="chip ok" style="padding:2px 8px">读写</span>';
      var pw = a.password_set ? '<span class="chip ok" style="padding:2px 8px">已设</span>' : '<span class="chip err" style="padding:2px 8px">未设</span>';
      var dl = a.redirect ? "302 到 CDN" : "本机代理";
      html += "<tr>" +
        "<td>" + name + '<div class="hint">' + esc(a.account || "") + "</div></td>" +
        '<td><code class="k">' + esc(a.url || a.path || "") + "</code></td>" +
        "<td>" + esc(a.user || "") + "</td>" +
        "<td>" + pw + "</td>" +
        "<td>" + ro + "</td>" +
        "<td>" + dl + "</td>" +
        '<td style="text-align:right;white-space:nowrap">' +
          (isCur ? "" : '<button class="sm ghost" data-dav-act="switch" data-id="' + esc(a.id) + '">切到这台</button> ') +
          '<button class="sm ghost" data-dav-act="copy" data-url="' + esc(a.url || "") + '" data-user="' + esc(a.user || "") + '">复制地址</button>' +
        "</td></tr>";
    }
    body.innerHTML = html;
  } catch (e) {
    body.innerHTML = '<tr><td colspan="7" class="empty">' + esc(e.message) + "</td></tr>";
  }
}

/* 总览里的快捷操作：切账号 / 复制地址+用户名 */
(function () {
  var body = $("davAllBody");
  if (!body) return;
  body.onclick = async function (ev) {
    var t = ev.target;
    while (t && t !== body && !(t.tagName === "BUTTON" && t.dataset && t.dataset.davAct)) t = t.parentNode;
    if (!t || t === body) return;
    if (t.dataset.davAct === "switch") { await selectAccount(t.dataset.id); showTab("dav"); return; }
    if (t.dataset.davAct === "copy") {
      var text = (t.dataset.url || "") + "\n用户名：" + (t.dataset.user || "");
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () { toast("地址和用户名已复制", "ok"); }, function () { prompt("复制：", text); });
      } else { prompt("复制：", text); }
    }
  };
})();

function davCopy(text, btn) {
  var done = function () {
    if (!btn) return;
    var old = btn.innerHTML;
    btn.innerHTML = "已复制";
    setTimeout(function () { btn.innerHTML = old; }, 1400);
  };
  var fallback = function () {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed"; ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); done(); } catch (e) { say("msgDav", "浏览器不让复制，请手动选中输入框里的内容", false); }
    document.body.removeChild(ta);
  };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(done).catch(fallback);
  } else {
    fallback();  // 飞牛控制台多半是 http，clipboard API 不可用，走 execCommand
  }
}

async function davSave(patch, btn, okText) {
  var body = Object.assign({
    enabled: $("davEnabled").checked,
    user: $("davUser").value,
    password: $("davPass").value,
    redirect: $("davRedir").checked
  }, patch || {});
  busy(btn, true);
  try {
    var d = await api("/api/dav/config", { method: "POST", body: JSON.stringify(body) });
    davShow(d.dav);
    say("msgDav", okText || (body.enabled ? "已保存，可以把上面的地址填进飞牛了" : "已保存，WebDAV 已关闭"), true);
  } catch (e) {
    say("msgDav", e.message, false);
  } finally {
    busy(btn, false);
  }
}

$("btnDavSave").onclick = function () { davSave(null, this); };
$("btnDavReset").onclick = function () {
  if (!confirm("重置密码后，飞牛里已经挂着的连接会失效，需要重新填一次。确定重置？")) return;
  davSave({ regenerate: true }, this, "密码已重置，请重新填进飞牛");
};
$("btnDavCopy").onclick = function () { davCopy($("davURL").value, this); };
$("btnDavCopyAuth").onclick = function () {
  davCopy("地址：" + $("davURL").value + "\n用户名：" + $("davUser").value + "\n密码：" + $("davPass").value, this);
};

/* ---------------- 状态刷新（含挂载状态） ---------------- */
async function refresh() {
  var d;
  try {
    d = await api("/api/status", { timeout: 12000 });
  } catch (e) {
    $("badgeMount").className = "chip err";
    $("badgeMount").innerHTML = '<i class="dot"></i><b>状态读取失败</b>';
    $("heroState").textContent = "无法读取状态";
    $("heroState").className = "txt off";
    $("heroDot").className = "sdot";
    say("msgCfg", e.message, false);
    return;
  }
  var st = d.status || {};
  cfg = d.config || {};
  webPassSet = !!cfg.web_password_set;
  refreshConsoleAuthHint();
  mounted = !!st.mounted;
  lastSt = st;
  accounts = d.accounts || [];
  if (!curAccount) curAccount = cfg.default_account || (accounts[0] && accounts[0].id) || "";
  if (curAccount && !acctExists(curAccount)) {
    curAccount = cfg.default_account || (accounts[0] && accounts[0].id) || "";
  }
  renderAcctSel();
  renderAcctTable();

  // 顶栏
  $("badgeMount").className = "chip " + (mounted ? "ok live" : "");
  $("badgeMount").innerHTML = '<i class="dot"></i><b>' + (mounted ? "已挂载" : "未挂载") + "</b>";
  var a0 = curAcctObj();
  var who = st.account || "已授权";
  if (a0 && a0.custom_name && accounts.length > 1) who = a0.custom_name + " · " + who;
  var acct = st.has_token ? ("账号 " + who) : "未授权";
  $("badgeAcct").className = "chip " + (st.has_token ? "" : "warn");
  $("badgeAcct").textContent = acct;
  $("badgeAcct").title = accounts.length > 1 ? "当前操作的是这台云盘；用左边的下拉切换账号" : "";
  $("badgeVer").textContent = "v" + (st.version || "-");

  var au = st.auth || {};
  if (!st.has_token) {
    $("badgeAuth").className = "chip warn";
    $("badgeAuth").textContent = "无授权";
    $("badgeAuth").title = "还没有授权。到「授权」标签页粘贴 Authorization 之后再挂载。";
    $("authInfo").textContent = "";
  } else {
    var days = au.expires_days || 0;
    var left = days >= 1 ? ("剩余 " + days.toFixed(1) + " 天") : "即将过期";
    $("badgeAuth").className = "chip " + (days >= 15 ? "ok" : "warn");
    $("badgeAuth").textContent = "授权 " + left;
    $("badgeAuth").title = AUTH_TIP;
    var when = au.expires_at ? ("到期 " + String(au.expires_at).slice(0, 10)) : "到期时间未知";
    var pr = au.last_refresh ? ("上次自动续期 " + String(au.last_refresh).slice(0, 16).replace("T", " ")) : "尚未自动续期";
    $("authInfo").textContent = when + "；" + pr + "；暂存占用 " +
      ((st.stage_bytes || 0) / 1048576).toFixed(1) + " MB / " + (st.stage_files || 0) + " 个文件";
    $("authInfo").title = AUTH_TIP;
  }

  // 状态卡
  var mp = cfg.mount_point || "";
  $("heroState").textContent = mounted ? "已挂载" : "未挂载";
  $("heroState").className = "txt" + (mounted ? "" : " off");
  $("heroDot").className = "sdot" + (mounted ? " on" : "");
  $("heroPath").textContent = mp || "（还没设置挂载位置）";
  $("heroPath").title = mp;
  var bits = [];
  bits.push('<span>账号 <b>' + esc(st.account || "—") + "</b></span>");
  bits.push('<span>模式 <b>' + (cfg.read_only ? "只读" : "读写") + "</b></span>");
  bits.push('<span>属主 <b>' + (cfg.uid || 0) + ":" + (cfg.gid || 0) + "</b></span>");
  if (st.has_token) bits.push('<span title="' + esc(AUTH_TIP) + '">授权剩余 <b>' + ((au.expires_days || 0)).toFixed(1) + " 天</b></span>");
  bits.push('<span>开机自动挂载 <b>' + (cfg.auto_mount === false ? "关" : "开") + "</b></span>");
  if (d.pid) bits.push('<span>进程 <b>PID ' + d.pid + "</b></span>");
  $("heroMeta").innerHTML = bits.join("");

  var hint = "";
  if (!st.has_token) {
    hint = "还没填授权。到「授权」标签页粘贴 Authorization 之后再挂载。";
  } else if (!mounted && mp) {
    hint = "点右边「挂载」就会把云盘接到 " + mp + "，飞牛「文件」里立刻可见。";
  } else if (mounted) {
    hint = "云盘内容已接到 " + mp + "：飞牛「文件」、SMB、影视刮削都能直接读。";
  } else {
    hint = "先在「挂载设置」里选一个位置。";
  }
  $("heroHint").textContent = hint;

  // 按钮随状态变：已挂载时「挂载」降级为普通按钮
  $("btnMount").className = mounted ? "" : "primary";
  $("btnRemount").disabled = !st.has_token;

  var strips = [];
  if (st.dead_mount) {
    strips.push('<div class="strip err"><b>挂载点失去响应：</b><span>' + esc(st.mount_point || "配置里的位置") +
      " 上有一个读不了的残留挂载（上一次进程被强制结束留下的）。点上方「挂载」会先清理掉它再重新挂上。</span></div>");
  }
  if (st.stray_mount) {
    strips.push('<div class="strip"><b>残留挂载：</b><span>' + esc(st.stray_mount) +
      " 还挂着上一次的云盘（配置里的位置是 " + esc(st.mount_point || "—") +
      '）。点右侧清理，或直接点「重新挂载」。</span><button id="btnCleanStray" class="sm" style="margin-left:auto;flex:0 0 auto">清理残留挂载</button></div>');
  }
  if (st.last_error) {
    strips.push('<div class="strip err"><b>最近一次错误：</b><span>' + esc(st.last_error) + "</span></div>");
  }
  $("lastErr").innerHTML = strips.join("");
  if (st.stray_mount) {
    $("btnCleanStray").onclick = async function () {
      var b = this;
      busy(b, true, "清理中…");
      try {
        await api("/api/unmount", { method: "POST", timeout: 40000 });
        toast("残留挂载已清理", "ok");
      } catch (e) { toast("清理失败：" + e.message, "err"); }
      busy(b, false);
      await refresh(); await loadLogs();
    };
  }

  $("footVer").textContent = st.version || "—";

  // 表单（挂载位置正在编辑时不要覆盖用户输入）
  if (document.activeElement !== $("f_mount")) $("f_mount").value = mp;
  $("f_ro").checked = !!cfg.read_only;
  $("f_uid").value = cfg.uid != null ? cfg.uid : 0;
  $("f_gid").value = cfg.gid != null ? cfg.gid : 0;
  $("f_ttl").value = cfg.dir_cache_ttl != null ? cfg.dir_cache_ttl : 60;
  $("f_part").value = cfg.upload_part_mb != null ? cfg.upload_part_mb : 100;
  $("f_cap").value = cfg.capacity_gb != null ? cfg.capacity_gb : 2048;
  $("f_ctype").value = cfg.client_type || "13";
  $("f_auto").checked = cfg.auto_mount !== false;
  $("f_port").value = cfg.web_port != null ? cfg.web_port : 0;
}

async function loadShares() {
  try {
    var d = await api("/api/shares", { timeout: 12000 });
    $("shareList").innerHTML = (d.paths || []).map(function (p) { return '<option value="' + esc(p) + '">'; }).join("");
  } catch (e) { /* 只是补全提示，失败无所谓 */ }
}

async function saveConfig(extra) {
  var patch = Object.assign({
    mount_point: $("f_mount").value.trim(),
    read_only: $("f_ro").checked,
    uid: Number($("f_uid").value || 0),
    gid: Number($("f_gid").value || 0),
    dir_cache_ttl: Number($("f_ttl").value || 60),
    upload_part_mb: Number($("f_part").value || 100),
    capacity_gb: Number($("f_cap").value || 2048),
    client_type: $("f_ctype").value,
    auto_mount: $("f_auto").checked
  }, extra || {});
  return api("/api/config", { method: "POST", body: JSON.stringify(patch), timeout: 30000 });
}

async function loadLogs() {
  if (!document.getElementById("tab-logs").classList.contains("act")) return;
  var q = "/api/logs?n=200";
  var wrap = $("logAcctWrap"), only = $("f_logAcct");
  if (wrap) wrap.style.display = accounts.length > 1 ? "" : "none";
  var who = "";
  if (only && only.checked) {
    var id = logAccount || curAccount;
    if (id) { q += "&account=" + encodeURIComponent(id); who = id; }
  }
  try {
    var d = await api(q, { timeout: 12000 });
    var pre = $("logs");
    pre.textContent = (d.lines || []).join("\n") || (d.filtered ? "（这个账号还没有日志）" : "（暂无日志）");
    var sub = $("logSub");
    if (sub) {
      var name = "";
      for (var i = 0; i < accounts.length; i++) if (accounts[i].id === who) name = accounts[i].name || accounts[i].id;
      sub.textContent = who ? ("只看账号 " + (name || who) + " · 最近 200 条") : "全部账号 · 最近 200 条";
    }
    if ($("f_follow").checked) pre.scrollTop = pre.scrollHeight;
  } catch (e) { $("logs").textContent = e.message; }
}

/* ---------------- 扫码登录 ---------------- */
// 二维码是服务端用 PNG 渲出来的，前端只负责显示 + 轮询；会话 id 是随机的，
// 真正的凭据（skey）留在服务端，不落盘。
var qrId = "", qrTimer = null, qrStopFlag = false, qrAutoStarted = false;

function qrSetImage(on) {
  $("qrImg").className = on ? "on" : "";
  $("qrPh").className = on ? "qrph off" : "qrph";
}

function qrHalt(msg) {
  qrStopFlag = true;
  if (qrTimer) { clearTimeout(qrTimer); qrTimer = null; }
  $("btnQrStart").disabled = false;
  if (msg) say("qrMsg", msg, false);
}

async function qrStart() {
  var b = $("btnQrStart");
  qrStopFlag = false;
  if (qrTimer) { clearTimeout(qrTimer); qrTimer = null; }
  busy(b, true, "生成中…");
  try {
    var d = await api("/api/auth/qr/start", { method: "POST", timeout: 45000 });
    qrId = d.id || "";
    $("qrImg").src = d.image;
    qrSetImage(true);
    var mins = Math.max(1, Math.round((d.expires_in || 300) / 60));
    say("qrMsg", "请用手机 App 扫左边的二维码（" + mins + " 分钟内有效）", true);
    qrPoll();
  } catch (e) {
    qrSetImage(false);
    say("qrMsg", "生成失败：" + e.message, false);
    toast("生成二维码失败", "err");
  }
  busy(b, false);
}

async function qrPoll() {
  if (qrStopFlag || !qrId) return;
  var again = true;
  try {
    var d = await api("/api/auth/qr/poll?id=" + encodeURIComponent(qrId), { method: "POST", timeout: 45000 });
    var st = d.status || "";
    if (st === "waiting") {
      say("qrMsg", "等手机扫码…", true);
    } else if (st === "scanned") {
      say("qrMsg", "已扫码，请在手机上点「确认登录」…", true);
    } else if (st === "confirmed") {
      again = false;
      qrStopFlag = true;
      qrId = "";
      qrSetImage(false);
      say("qrMsg", "登录成功：" + (d.account || "账号已绑定") + "，授权已保存，正在挂载…", true);
      toast("扫码登录成功", "ok");
      await refresh();
      await loadLogs();
      // 后台挂载需要几秒，过一会儿再刷新两次状态，别让用户以为卡住了
      setTimeout(function () { if (!document.hidden) refresh(); }, 3000);
      setTimeout(function () { if (!document.hidden) refresh(); }, 9000);
    } else if (st === "expired") {
      again = false;
      qrSetImage(false);
      qrHalt(d.message || "二维码已过期，请重新生成");
    } else if (st === "cancelled") {
      again = false;
      qrSetImage(false);
      qrHalt(d.message || "手机上取消了登录，请重新生成");
    } else {
      again = false;
      qrHalt(d.message || "登录未完成，请重新生成二维码");
    }
  } catch (e) {
    say("qrMsg", "状态查询失败：" + e.message, false);
  }
  if (again && !qrStopFlag) qrTimer = setTimeout(qrPoll, 2000);
}

$("btnQrStart").onclick = function () { qrStart(); };
$("btnQrStop").onclick = function () { qrHalt("已停止，重新生成可以再来一次"); qrSetImage(false); };

/* ---------------- 授权 ---------------- */
(function () {
  var btn = $("btnCopyBm");
  if (!btn) return;
  btn.onclick = function () {
    var code = ($("bmCode") && $("bmCode").textContent || "").trim();
    var ok = function () { toast("书签代码已复制：存成书签后在 139 页面点一下", "ok"); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(code).then(ok, function () { prompt("复制这段书签代码：", code); });
    } else { prompt("复制这段书签代码：", code); }
  };
})();

$("btnSaveAuth").onclick = async function () {
  var v = $("f_auth").value.trim();
  if (!v) { say("msgAuth", "请输入 Authorization", false); return; }
  var b = this;
  busy(b, true, "保存中…");
  try {
    await saveConfig({ authorization: v });
    $("f_auth").value = "";
    say("msgAuth", "授权已保存", true);
    toast("授权已保存", "ok");
    await refresh(); await loadLogs();
  } catch (e) { say("msgAuth", e.message, false); toast("保存授权失败：" + e.message, "err"); }
  busy(b, false);
};

$("btnRefreshAuth").onclick = async function () {
  var b = this;
  busy(b, true, "刷新中…");
  say("msgAuth", "正在刷新令牌…");
  try {
    var r = await api("/api/auth/refresh", { method: "POST", timeout: 70000 });
    say("msgAuth", r.message || "令牌已刷新", true);
    toast(r.message || "令牌已刷新", "ok");
    await refresh();
  } catch (e) { say("msgAuth", "刷新失败：" + e.message, false); toast("刷新令牌失败：" + e.message, "err"); }
  busy(b, false);
};

$("btnTest").onclick = async function () {
  var b = this;
  busy(b, true, "连接中…");
  say("msgAuth", "正在连接移动云盘…");
  try {
    var d = await api("/api/test", { method: "POST", timeout: 40000 });
    say("msgAuth", "连接成功，账号 " + (d.account || "-") + "，根目录 " + d.entries + " 项", true);
    toast("云盘连接正常，根目录 " + d.entries + " 项", "ok");
    showTab("browse");
    browse("/");
  } catch (e) { say("msgAuth", "连接失败：" + e.message, false); toast("连接失败：" + e.message, "err"); }
  busy(b, false);
};

/* ---------------- 挂载动作 ---------------- */
$("btnSave").onclick = async function () {
  var b = this;
  busy(b, true, "保存中…");
  try { await saveConfig(); say("msgCfg", "设置已保存", true); toast("设置已保存", "ok"); await refresh(); }
  catch (e) { say("msgCfg", e.message, false); toast("保存失败：" + e.message, "err"); }
  busy(b, false);
};

$("btnMount").onclick = async function () {
  var b = this;
  busy(b, true, "挂载中…");
  say("msgCfg", "正在挂载…");
  try {
    await saveConfig();                     // 先把当前设置（含挂载位置）存下来
    await api("/api/mount", { method: "POST", timeout: 100000 });
    say("msgCfg", "挂载成功", true);
  } catch (e) {
    say("msgCfg", "挂载失败：" + e.message, false);
  }
  busy(b, false);
  await refresh(); await loadLogs();
  if (mounted) toast("已挂载：" + (cfg.mount_point || ""), "ok");
  else toast($("msgCfg").textContent || "挂载没有成功，看状态卡上的错误", "err");
};

$("btnRemount").onclick = async function () {
  var b = this;
  busy(b, true, "重新挂载中…");
  say("msgCfg", "正在重新挂载…");
  try {
    await saveConfig();
    await api("/api/remount", { method: "POST", timeout: 130000 });
    say("msgCfg", "已重新挂载", true);
  } catch (e) { say("msgCfg", "重新挂载失败：" + e.message, false); }
  busy(b, false);
  await refresh(); await loadLogs();
  if (mounted) toast("已重新挂载：" + (cfg.mount_point || ""), "ok");
  else toast($("msgCfg").textContent || "重新挂载没有成功", "err");
};

$("btnUnmount").onclick = async function () {
  var b = this;
  if (!confirm("确定要卸载 " + (cfg.mount_point || "") + " 吗？卸载后「文件」里就看不到云盘内容了（配置和授权都会保留）。")) return;
  busy(b, true, "卸载中…");
  try {
    await api("/api/unmount", { method: "POST", timeout: 40000 });
    toast("已卸载", "ok");
  } catch (e) { toast("卸载失败：" + e.message, "err"); say("msgCfg", e.message, false); }
  busy(b, false);
  await refresh(); await loadLogs();
};

$("btnRefreshAll").onclick = async function () {
  var b = this;
  b.classList.add("spin");
  await refresh(); await loadLogs();
  setTimeout(function () { b.classList.remove("spin"); }, 350);
};

$("btnCopyPath").onclick = function () {
  var t = cfg.mount_point || "";
  if (!t) { toast("还没有挂载位置", "warn"); return; }
  var ta = document.createElement("textarea");
  ta.value = t; ta.style.position = "fixed"; ta.style.opacity = "0";
  document.body.appendChild(ta); ta.select();
  var ok = false;
  try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
  ta.remove();
  toast(ok ? ("已复制：" + t) : ("路径：" + t), ok ? "ok" : "warn");
};

/* ---------------- 云盘浏览 ---------------- */
// 这一页直接读写云端：新建文件夹 / 上传 / 下载 / 重命名 / 删除（进回收站）。
// 目录用 id 记（139 的 id 就是路径），remoteMap 存当前层的条目，操作按钮靠它反查名字。
var remoteDir = "";
var remoteStack = [];
var remoteMap = {};

function remoteOps(e) {
  var out = "";
  if (!e.is_dir) out += '<button class="qlink" data-act="dl" data-id="' + esc(e.id) + '" title="直接从云端下载（302 到签名链接）">下载</button>';
  out += '<button class="qlink" data-act="mv" data-id="' + esc(e.id) + '">重命名</button>';
  out += '<button class="qlink" data-act="rm" data-id="' + esc(e.id) + '">删除</button>';
  return '<td style="text-align:right; white-space:nowrap">' + out + "</td>";
}

async function browse(dir) {
  $("browseBody").innerHTML = '<tr><td colspan="4" class="empty">加载中…</td></tr>';
  try {
    var d = await api("/api/remote/list?dir=" + encodeURIComponent(dir), { timeout: 60000 });
    var ents = d.entries || [];
    remoteDir = dir || "";
    remoteMap = {};
    ents.forEach(function (e) { remoteMap[e.id] = e; });
    var rows = ents.filter(function (e) { return e.is_dir; }).map(function (e) {
      return '<tr><td><span class="fname"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" stroke-width="1.9"><path d="M3 7a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.4.6L11.4 7H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/></svg>' +
        '<span class="linklike" data-id="' + esc(e.id) + '" data-name="' + esc(e.name) + '">' + esc(e.name) + "</span></span></td><td>目录</td><td style=\"text-align:right\">—</td>" +
        remoteOps(e) + "</tr>";
    });
    var files = ents.filter(function (e) { return !e.is_dir; }).map(function (e) {
      return '<tr><td><span class="fname"><span title="' + esc(e.name) + '">' + esc(e.name) + "</span></span></td><td>文件</td><td style=\"text-align:right\">" + fmtSize(e.size) + "</td>" +
        remoteOps(e) + "</tr>";
    });
    $("browseBody").innerHTML = rows.concat(files).join("") || '<tr><td colspan="4" class="empty">空目录</td></tr>';
  } catch (e) {
    $("browseBody").innerHTML = '<tr><td colspan="4" class="empty" style="color:var(--err)">' + esc(e.message) + "</td></tr>";
  }
}

async function remoteAction(act, id) {
  var e = remoteMap[id] || {};
  if (act === "dl") {
    window.location.href = "/api/remote/download?id=" + encodeURIComponent(id);
    return;
  }
  if (act === "mv") {
    var n = prompt("重命名「" + (e.name || "") + "」为：", e.name || "");
    if (n === null) return;
    n = n.trim();
    if (!n || n === e.name) return;
    try {
      var r = await api("/api/remote/rename", { method: "POST", timeout: 40000, body: JSON.stringify({ id: id, name: n }) });
      toast(r.message || "已重命名", "ok");
    } catch (err) { toast("重命名失败：" + err.message, "err"); }
    await browse(remoteDir);
    return;
  }
  if (act === "rm") {
    if (!confirm("把「" + (e.name || "") + "」移到云盘回收站？\n\n· 挂载目录里最多 60 秒后消失\n· 想找回就去 139 网页版或 App 的回收站还原")) return;
    try {
      var r2 = await api("/api/remote/trash", { method: "POST", timeout: 60000, body: JSON.stringify({ ids: [id] }) });
      toast(r2.message || "已删除", "ok");
    } catch (err2) { toast("删除失败：" + err2.message, "err"); }
    await browse(remoteDir);
  }
}

$("browseBody").onclick = function (ev) {
  var b = ev.target.closest("[data-act]");
  if (b) { ev.stopPropagation(); remoteAction(b.dataset.act, b.dataset.id); return; }
  var a = ev.target.closest(".linklike");
  if (a) {
    remoteStack.push({ id: remoteDir, name: $("curDir").textContent });
    remoteDir = a.dataset.id;
    $("curDir").textContent = a.dataset.name;
    browse(remoteDir);
  }
};

$("btnUp").onclick = function () {
  if (!remoteStack.length) return;
  var prev = remoteStack.pop();
  $("curDir").textContent = prev.id ? (prev.name || "/") : "/";
  browse(prev.id);
};

$("btnRemoteMkdir").onclick = async function () {
  var n = prompt("在当前目录新建文件夹，名字：", "");
  if (n === null) return;
  n = n.trim();
  if (!n) return;
  var b = this;
  busy(b, true, "创建中…");
  try {
    var d = await api("/api/remote/mkdir", { method: "POST", timeout: 40000, body: JSON.stringify({ parent: remoteDir, name: n }) });
    toast(d.message || "已创建", "ok");
    await browse(remoteDir);
  } catch (e) { toast("新建失败：" + e.message, "err"); }
  busy(b, false);
};

$("btnRemoteUpload").onclick = function () { $("f_upload").click(); };

$("f_upload").onchange = async function () {
  var files = this.files || [];
  if (!files.length) return;
  var b = $("btnRemoteUpload");
  busy(b, true, "上传中…");
  var ok = 0, bad = 0;
  for (var i = 0; i < files.length; i++) {
    var fd = new FormData();
    fd.append("file", files[i]);
    try {
      // 上传不设超时：大文件要走分片，几十 GB 都可能
      var res = await fetch(withAcct("/api/remote/upload?parent=" + encodeURIComponent(remoteDir)), { method: "POST", body: fd });
      var d = await res.json().catch(function () { return {}; });
      if (!res.ok || d.ok === false) throw new Error(d.error || ("HTTP " + res.status));
      ok++;
      toast(d.message || ("已上传 " + files[i].name), "ok");
    } catch (e) {
      bad++;
      toast("上传失败：" + files[i].name + "：" + e.message, "err");
    }
  }
  $("f_upload").value = "";
  busy(b, false);
  if (ok || bad) toast("上传结束：成功 " + ok + " 个" + (bad ? ("，失败 " + bad + " 个") : ""), bad ? "warn" : "ok");
  await browse(remoteDir);
};

/* ---------------- 位置选择器 ---------------- */
var pickSel = "";
var pickCache = {};
var pickExp = {};

function showPicker(on) { $("pickModal").style.display = on ? "flex" : "none"; }

function kidsOf(p) {
  var all = pickCache[p] || [];
  if ($("pickShowSys").checked) return all;
  return all.filter(function (e) { return !e.system; });
}

async function pickFetch(p) {
  var key = p || "/";
  if (pickCache[key]) return pickCache[key];
  var d = await api("/api/fs/list?path=" + encodeURIComponent(key), { timeout: 20000 });
  pickCache[key] = d.entries || [];
  return pickCache[key];
}

function treeRows(p, depth, out) {
  kidsOf(p).forEach(function (e) {
    var exp = !!pickExp[e.path];
    out.push('<div class="tnode' + (pickSel === e.path ? " sel" : "") + '" data-p="' + esc(e.path) +
      '" style="padding-left:' + (8 + depth * 15) + 'px">' +
      '<span class="caret" data-toggle="' + esc(e.path) + '">' + (exp ? "▾" : "▸") + "</span>" +
      '<span class="tname">' + esc(e.name) + "</span>" +
      (e.is_mount && !e.is_volume ? '<span class="tag">已挂载</span>' : "") +
      (e.system ? '<span class="tag sys">系统</span>' : "") +
      "</div>");
    if (exp) treeRows(e.path, depth + 1, out);
  });
}

function renderTree() {
  var out = [];
  treeRows("/", 0, out);
  $("pickTree").innerHTML = out.join("") || '<div class="tnode" style="color:var(--muted)">（没有可选的存储卷）</div>';
  $("pickTree").querySelectorAll(".tnode").forEach(function (n) {
    n.onclick = function (ev) {
      var t = ev.target.closest("[data-toggle]");
      if (t && t.dataset.toggle) { ev.stopPropagation(); toggleExpand(t.dataset.toggle); return; }
      if (n.dataset.p) pickSelect(n.dataset.p);
    };
  });
}

function renderList() {
  var cur = pickSel || "/";
  $("pickPath").textContent = cur === "/" ? "存储空间（点一个卷进去）" : cur;
  var rows = [];
  if (cur !== "/") {
    var up = cur.split("/").slice(0, -1).join("/") || "/";
    rows.push('<div class="lrow up" data-p="' + esc(up) + '"><span class="lname">⬆ 上一级</span></div>');
  }
  kidsOf(cur).forEach(function (e) {
    var st = e.is_mount ? "已是挂载点" : (e.empty ? "空目录" : "非空");
    rows.push('<div class="lrow" data-p="' + esc(e.path) + '"><span class="lname">' + esc(e.name) + "</span>" +
      '<span class="lmeta">' + esc(e.owner || "-") + "</span><span class=\"lmeta\">" + st + "</span></div>");
  });
  $("pickBody").innerHTML = rows.join("") || '<div class="lrow"><span class="lname" style="color:var(--muted)">（没有子目录）</span></div>';
  $("pickBody").querySelectorAll(".lrow").forEach(function (r) {
    if (!r.dataset.p) return;
    r.onclick = function () { pickSelect(r.dataset.p); };
  });
}

async function toggleExpand(p) {
  if (pickExp[p]) { delete pickExp[p]; renderTree(); return; }
  say("pickMsg", "正在读取 " + p + " …");
  try { await pickFetch(p); } catch (e) { say("pickMsg", e.message, false); return; }
  pickExp[p] = true;
  say("pickMsg", "");
  renderTree();
}

var PERSONAL_ROOT_RE = /^\/vol[0-9]+\/[0-9]{4,}$/;   // /vol4/1000 这种个人空间根目录

function setSel(text, cls) {
  $("pickSel").textContent = text;
  $("pickSel").className = "sel " + (cls || "");
}

async function pickSelect(p) {
  pickSel = p || "/";
  var readErr = "";
  if (pickSel !== "/") {
    try { await pickFetch(pickSel); } catch (e) { readErr = e.message; }
    pickExp[pickSel] = true;
  }
  renderTree(); renderList();
  if (pickSel === "/") {
    setSel("还没选目录（不能挂在存储卷根）");
    return;
  }
  var warn = "", msg = "", masked = false, isCur = false;
  try {
    var c = await api("/api/mountpoint/check", { method: "POST", body: JSON.stringify({ path: pickSel, allow_nonempty: true }), timeout: 20000 });
    var inf = c.info || {};
    masked = !!inf.masked;
    isCur = !!inf.is_mount && !masked;
    if (masked) {
      warn = " ｜ 它在当前挂载点里面（现在看到的是云盘内容）：会先卸下原来的位置，再建/挂真实目录";
    } else if (isCur) {
      warn = " ｜ 这就是当前的挂载点";
      if (PERSONAL_ROOT_RE.test(pickSel)) {
        warn += " ⚠ 它遮住了这里原有的文件，建议挂到子文件夹 " + pickSel + "/移动云盘（勾上「目录不存在时自动创建」再点「挂到这里」）";
        if (!$("pickNewName").value) $("pickNewName").value = "移动云盘";
      } else if (!inf.empty) {
        warn += "（" + inf.entries + " 项，都是云盘里的内容）";
      }
    } else if (PERSONAL_ROOT_RE.test(pickSel)) {
      warn = " ｜ ⚠ 这是个人空间的根目录，挂在它上面会遮住你原有的文件。建议挂在它下面的子文件夹，例如 " + pickSel + "/移动云盘";
      if (!$("pickNewName").value) $("pickNewName").value = "移动云盘";
    } else if (inf.exists && !inf.empty) {
      warn = " ｜ ⚠ 这个目录里已有 " + inf.entries + " 项内容，挂上去会被遮住（要挂就得勾上「我确认会遮住…」）";
    }
  } catch (e) { msg = e.message; }
  $("pickPathInput").value = pickSel;
  setSel("已选：" + pickSel + warn, (masked || PERSONAL_ROOT_RE.test(pickSel)) ? "warn" : (isCur ? "ok" : ""));
  if (isCur) return;
  if (msg) say("pickMsg", msg, false);
  else if (readErr) say("pickMsg", masked ? "这里的内容看不到是正常的（它在当前挂载点里面）：挂载时会先卸下原位置" : readErr, false);
  else say("pickMsg", "");
}

async function openPath(p) {
  p = (p || "/").replace(/\/+$/, "") || "/";
  var bad = "";
  try {
    await pickFetch("/");
  } catch (e) { bad = e.message; }
  if (!bad && p !== "/") {
    var acc = "";
    var parts = p.split("/").filter(Boolean);
    for (var i = 0; i < parts.length; i++) {
      acc += "/" + parts[i];
      try { await pickFetch(acc); } catch (e) { bad = e.message; break; }
      pickExp[acc] = true;
    }
  }
  if (p === "/") pickExp["/"] = true;
  await pickSelect(p);
  if (bad) say("pickMsg", bad, false);
}

async function pickLinks() {
  try {
    var d = await api("/api/shares", { timeout: 15000 });
    $("pickLinks").innerHTML = (d.links || []).map(function (l) {
      return '<button class="qlink" data-dir="' + esc(l.path) + '" title="' + esc(l.hint || "") + '">' + esc(l.label) + "</button>";
    }).join("");
    $("pickLinks").querySelectorAll("button").forEach(function (b) {
      b.onclick = function () { openPath(b.dataset.dir); };
    });
  } catch (e) { /* 快捷入口失败不影响手动浏览 */ }
}

async function pickApply() {
  var path = pickSel;
  if (pickCb) {
    if (!path || path === "/") { say("pickMsg", "先选一个目录（不能用存储卷根）", false); return; }
    var cb = pickCb;
    pickCb = null;
    showPicker(false);
    cb(path);
    return;
  }
  if (!path || path === "/") { say("pickMsg", "先在左边的树里选一个目录（不能挂在存储卷根上）", false); return; }
  var force = $("pickForce").checked;
  var b = $("pickApply");
  say("pickMsg", "正在检查 " + path + " …");
  var masked = false;
  try {
    var c = await api("/api/mountpoint/check", { method: "POST", body: JSON.stringify({ path: path, allow_nonempty: force }), timeout: 20000 });
    var inf = c.info || {};
    masked = !!inf.masked;
    if (inf.exists === false && !$("pickCreate").checked) {
      say("pickMsg", masked
        ? "这个位置在当前挂载点里面，真实目录要卸下来才知道。勾上「目录不存在时自动创建」就能直接挂（会先卸下原位置，再建好目录）。"
        : "目录不存在：" + path + "。可以点「在选中目录里新建文件夹」，或勾选「目录不存在时自动创建」。", false);
      return;
    }
  } catch (e) { say("pickMsg", e.message, false); return; }

  busy(b, true, "切换中…");
  say("pickMsg", masked ? "正在切换挂载位置…（会先卸下原位置，再建好目录并挂上）" : "正在切换挂载位置…（先卸载原位置）");
  try {
    var r = await api("/api/mountpoint/apply", { method: "POST", timeout: 190000, body: JSON.stringify({
      path: path, create: $("pickCreate").checked, allow_nonempty: force, follow_owner: true }) });
    $("f_mount").value = path;
    if (r.config) { $("f_uid").value = r.config.uid; $("f_gid").value = r.config.gid; }
    showPicker(false);
    say("msgCfg", "挂载位置已切换：" + path, true);
    toast("已挂到 " + path + "，飞牛「文件」里现在就能看到", "ok");
    await refresh(); await loadLogs();
  } catch (e) { say("pickMsg", e.message, false); toast("切换失败：" + e.message, "err"); }
  busy(b, false);
}

var pickCb = null;   // 非空时，选完目录回填给调用者（STRM 映射用），不触发挂载

function openPicker(cb) {
  pickCb = (typeof cb === "function") ? cb : null;
  showPicker(true);
  say("pickMsg", "");
  pickCache = {};   // 每次打开都重新读，避免卸载/新建之后看到旧内容
  pickExp = {};
  pickLinks();
  var start = (cfg.mount_point && cfg.mount_point.indexOf("/vol") === 0) ? cfg.mount_point : "/";
  openPath(start);
}

$("btnPick").onclick = function () { openPicker(); };
$("btnPick2").onclick = function () { openPicker(); };
$("pickClose").onclick = function () { showPicker(false); };
$("pickApply").onclick = pickApply;
$("pickShowSys").onchange = function () { renderTree(); renderList(); };
$("pickGo").onclick = function () {
  var v = $("pickPathInput").value.trim();
  if (!v) { say("pickMsg", "请输入路径", false); return; }
  say("pickMsg", "");
  pickCache = {};
  openPath(v);
};
$("pickPathInput").addEventListener("keydown", function (ev) {
  if (ev.key === "Enter") { ev.preventDefault(); $("pickGo").click(); }
});
$("pickReload").onclick = async function () {
  pickCache = {};
  say("pickMsg", "已重新读取目录");
  await openPath(pickSel || "/");
};
document.addEventListener("keydown", function (ev) { if (ev.key === "Escape") showPicker(false); });
$("pickMkdir").onclick = async function () {
  var name = $("pickNewName").value.trim();
  if (!name) { say("pickMsg", "请输入文件夹名", false); return; }
  if (!pickSel || pickSel === "/") { say("pickMsg", "先在左边的树里选一个目录，新文件夹会建在它里面", false); return; }
  var b = this;
  busy(b, true, "创建中…");
  try {
    var d = await api("/api/fs/mkdir", { method: "POST", timeout: 25000, body: JSON.stringify({ parent: pickSel, name: name }) });
    say("pickMsg", "已创建 " + d.path, true);
    $("pickNewName").value = "";
    delete pickCache[pickSel];
    pickExp[pickSel] = true;
    await openPath(d.path);
  } catch (e) { say("pickMsg", e.message, false); }
  busy(b, false);
};

/* ---------------- 启动 ---------------- */
refresh().then(function () {
  // 没授权就先把人带到「授权」页，省得对着挂载按钮发呆
  if (!lastSt || !lastSt.has_token) showTab("auth");
});
loadShares();
setInterval(function () { if (!document.hidden) refresh(); }, 5000);
setInterval(function () { if (!document.hidden) loadLogs(); }, 15000);
document.addEventListener("visibilitychange", function () { if (!document.hidden) refresh(); });
</script>
</body>
</html>
`
