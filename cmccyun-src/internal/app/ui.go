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
      <button class="tab" data-tab="auth">授权</button>
      <button class="tab" data-tab="browse">云盘浏览</button>
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
        </div>
      </div>
    </section>

    <!-- ===== 授权 ===== -->
    <section class="panel" id="tab-auth">
      <div class="card">
        <div class="hd"><h2>授权（Authorization）</h2><span class="sub">约 30 天有效期，应用会自动续期</span></div>
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
          <div class="actions">
            <button id="btnSaveAuth" class="primary">保存授权</button>
            <button id="btnRefreshAuth" title="立即向云盘服务端续期一次：成功后续期天数回到约 30 天，并把新授权写回配置">刷新令牌</button>
            <button id="btnTest">测试连接</button>
          </div>
          <div id="msgAuth" class="msg"></div>
          <div class="hint" id="authInfo" style="margin-top:6px" title=""></div>
          <div class="hint" style="margin-top:12px; padding-top:11px; border-top:1px dashed var(--line)">
            <b>顶栏「授权 剩余 N 天」指的是什么？</b>
            它是<b>这条 Authorization 自身</b>的剩余有效期，由云盘服务端写死在令牌里（约 30 天），
            <b>不是</b>会员到期日、也不是本地估算 —— 每天自然递减，不用管它。
            剩余不足 <b>15 天</b>时应用每 6 小时会自动续期一次，成功后回到约 30 天（见上方「上次自动续期」）；
            任何请求遇到鉴权失败也会立刻续期并重试一次。
            只有续期本身失败（例如网页端已退出登录、改过密码）时才需要重新抓一条 Authorization 粘进来。
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 云盘浏览 ===== -->
    <section class="panel" id="tab-browse">
      <div class="card">
        <div class="hd">
          <h2>云盘浏览</h2>
          <span class="sub">用来确认授权有效、云盘内容可读（这里看的是云端，不是挂载点）</span>
          <span class="grow"></span>
          <button id="btnUp" class="sm">返回上级</button>
        </div>
        <div class="bd">
          <div class="row" style="margin-bottom:11px">
            <span class="crumb">当前目录：<span id="curDir">/</span></span>
          </div>
          <div class="scroll">
            <table>
              <thead><tr><th>名称</th><th>类型</th><th style="text-align:right">大小</th></tr></thead>
              <tbody id="browseBody"><tr><td colspan="3" class="empty">尚未加载</td></tr></tbody>
            </table>
          </div>
        </div>
      </div>
    </section>

    <!-- ===== 日志 ===== -->
    <section class="panel" id="tab-logs">
      <div class="card">
        <div class="hd"><h2>运行日志</h2><span class="sub">最近 200 条</span></div>
        <div class="bd">
          <pre id="logs">加载中…</pre>
          <div class="actions">
            <button id="btnLogs" class="sm">刷新日志</button>
            <div class="checkline"><input id="f_follow" type="checkbox" checked /> <span class="hint">自动刷新并滚到最新</span></div>
          </div>
        </div>
      </div>
    </section>

  </div>
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

/* ---------------- 请求（带超时，绝不无限转圈） ---------------- */
async function api(path, opts) {
  opts = opts || {};
  var ms = opts.timeout || 25000;
  var ac = new AbortController();
  var timer = setTimeout(function () { ac.abort(); }, ms);
  try {
    var req = Object.assign({ headers: { "Content-Type": "application/json" }, signal: ac.signal }, opts);
    var res = await fetch(path, req);
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
  "这是你粘贴的 Authorization（网页登录态）自身的剩余有效期，由云盘服务端写死在令牌里，约 30 天。",
  "不是会员到期日，也不是本地估算。",
  "",
  "· 只是个倒计时，每天自然递减，无需理会；",
  "· 剩余不足 15 天时，应用每 6 小时会自动续期一次，成功后回到约 30 天；",
  "· 任何请求遇到鉴权失败，也会立刻强制续期并重试一次；",
  "· 只有续期本身失败（例如网页端已退出登录、改过密码）时，才需要重新抓一条 Authorization 粘进来。",
  "",
  "也可以点「刷新令牌」立即续期一次（可用来确认续期可用）。"
].join("\n");

/* ---------------- 标签页 ---------------- */
function showTab(name) {
  var tabs = document.querySelectorAll(".tab");
  for (var i = 0; i < tabs.length; i++) {
    tabs[i].classList.toggle("act", tabs[i].dataset.tab === name);
  }
  var panels = document.querySelectorAll(".panel");
  for (var j = 0; j < panels.length; j++) {
    panels[j].classList.toggle("act", panels[j].id === "tab-" + name);
  }
  if (name === "logs") loadLogs();
  if (name === "browse" && !browseLoaded && lastSt && lastSt.has_token) { browseLoaded = true; browse("/"); }
}
document.querySelectorAll(".tab").forEach(function (b) {
  b.onclick = function () { showTab(b.dataset.tab); };
});

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
  mounted = !!st.mounted;
  lastSt = st;

  // 顶栏
  $("badgeMount").className = "chip " + (mounted ? "ok live" : "");
  $("badgeMount").innerHTML = '<i class="dot"></i><b>' + (mounted ? "已挂载" : "未挂载") + "</b>";
  var acct = st.has_token ? ("账号 " + (st.account || "已授权")) : "未授权";
  $("badgeAcct").className = "chip " + (st.has_token ? "" : "warn");
  $("badgeAcct").textContent = acct;
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
  try {
    var d = await api("/api/logs?n=200", { timeout: 12000 });
    var pre = $("logs");
    pre.textContent = (d.lines || []).join("\n") || "（暂无日志）";
    if ($("f_follow").checked) pre.scrollTop = pre.scrollHeight;
  } catch (e) { $("logs").textContent = e.message; }
}

/* ---------------- 授权 ---------------- */
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
async function browse(dir) {
  $("browseBody").innerHTML = '<tr><td colspan="3" class="empty">加载中…</td></tr>';
  try {
    var d = await api("/api/remote/list?dir=" + encodeURIComponent(dir), { timeout: 60000 });
    var ents = d.entries || [];
    var rows = ents.filter(function (e) { return e.is_dir; }).map(function (e) {
      return '<tr><td><span class="fname"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" stroke-width="1.9"><path d="M3 7a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.4.6L11.4 7H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/></svg>' +
        '<span class="linklike" data-id="' + esc(e.id) + '" data-name="' + esc(e.name) + '">' + esc(e.name) + "</span></span></td><td>目录</td><td style=\"text-align:right\">—</td></tr>";
    });
    var files = ents.filter(function (e) { return !e.is_dir; }).map(function (e) {
      return '<tr><td><span class="fname"><span>' + esc(e.name) + "</span></span></td><td>文件</td><td style=\"text-align:right\">" + fmtSize(e.size) + "</td></tr>";
    });
    $("browseBody").innerHTML = rows.concat(files).join("") || '<tr><td colspan="3" class="empty">空目录</td></tr>';
    $("browseBody").querySelectorAll(".linklike").forEach(function (a) {
      a.onclick = function () {
        dirStack.push(curDir);
        curDir = a.dataset.id;
        $("curDir").textContent = a.dataset.name;
        browse(curDir);
      };
    });
  } catch (e) {
    $("browseBody").innerHTML = '<tr><td colspan="3" class="empty" style="color:var(--err)">' + esc(e.message) + "</td></tr>";
  }
}

$("btnUp").onclick = function () {
  if (!dirStack.length) return;
  curDir = dirStack.pop();
  $("curDir").textContent = curDir === "/" ? "/" : curDir;
  browse(curDir);
};
$("btnLogs").onclick = loadLogs;

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

function openPicker() {
  showPicker(true);
  say("pickMsg", "");
  pickCache = {};   // 每次打开都重新读，避免卸载/新建之后看到旧内容
  pickExp = {};
  pickLinks();
  var start = (cfg.mount_point && cfg.mount_point.indexOf("/vol") === 0) ? cfg.mount_point : "/";
  openPath(start);
}

$("btnPick").onclick = openPicker;
$("btnPick2").onclick = openPicker;
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
