document.body.parentNode.id = "educoder";
// if(document.domain !== "localhost"){
//   var iframe = document.createElement('iframe');
//   if (
//     document.domain === 'www.tokcoder.com' ||
//     document.domain === 'tokcoder.com'
//   ) {
//     iframe.src = 'https://www.educoder.net/autologin.html?t=' + Date.now();
//   } else {
//     iframe.src = 'https://www.tokcoder.com/autologin.html?t=' + Date.now();
//   }
//   iframe.sandbox =
//     'allow-scripts allow-same-origin allow-top-navigation allow-forms allow-popups allow-pointer-lock allow-popups-to-escape-sandbox allow-presentation allow-top-navigation-by-user-activation';
//   iframe.allow = 'geolocation document-domain';
//   iframe.style.position = 'absolute';
//   iframe.style.top = '-1000px';
//   iframe.style.opacity = 0;
//   iframe.style.visibility = 'hidden';
//   iframe.style.zIndex = -1;
//   document.body.appendChild(iframe);
//   iframe.onload = function() {
//     iframe.contentWindow.postMessage(
//       { type: 'cookie', data: document.cookie },
//       '*',
//     );
//   };

// }

window.gtag = new Function;
function setTop() {
    if ((location.host.indexOf("ilearning") > -1 || location.host.indexOf("i-learning") > -1) && location.pathname.indexOf("/tasks") === -1) {
        document.getElementById("root").style.paddingTop = "60px"
    }
    // saia默认进入竞赛  
    if(document.domain === "saia.educoder.net" && location.pathname === "/"){
      location.href = "/competitions/shszyjnds-2023/list"
    }
}
// window.addEventListener('popstate', function () {
//     setTop();
// })
setTop();

const _historyWrap = function(type) {
  const orig = history[type];
  const e = new Event(type);
  return function() {
    const rv = orig.apply(this, arguments);
    e.arguments = arguments;
    window.dispatchEvent(e);
    return rv;
  };
};
history.pushState = _historyWrap('pushState');
history.replaceState = _historyWrap('replaceState');

window.addEventListener('pushState', function(e) {
  setTop();
});
window.addEventListener('replaceState', function(e) {
  setTop();
});
if (window.ENV !== 'dev') {
  console.log = new Function;
}
function IEVersion() {
  var userAgent = navigator.userAgent; //取得浏览器的userAgent字符串  
  var isIE = userAgent.indexOf("compatible") > -1 && userAgent.indexOf("MSIE") > -1; //判断是否IE<11浏览器  
  var isEdge = userAgent.indexOf("Edge") > -1 && !isIE; //判断是否IE的Edge浏览器  
  var isIE11 = userAgent.indexOf('Trident') > -1 && userAgent.indexOf("rv:11.0") > -1;
  if (isIE || isIE11) {
      window.location.replace('/upgrade-browser.html');
  }
}
IEVersion();

function getChromeVersion() {
  var arr = navigator.userAgent.split(' ');
  var chromeVersion = '';
  for (var i = 0; i < arr.length; i++) {
      if (/chrome/i.test(arr[i]))
          chromeVersion = arr[i]
  }
  if (chromeVersion) {
      return Number(chromeVersion.split('/')[1].split('.')[0]);
  } else {
      return false;
  }
}
if (getChromeVersion()) {
  var version = getChromeVersion();
  if (!!version && version < 70) {
      window.location.replace('/upgrade-browser.html');
  }
}


window.addEventListener("storage", function (e) {
  if(e.key === "loginOut"){
    if(this.location.pathname.indexOf("/exercise") > -1 && this.location.pathname.indexOf("/users") > -1 )
      window.location.href = "/"
    else 
      window.location.reload();
  }
})

// 监听localstroage变化
;(function wrapLocalStorage() {
  const originalSetItem = Storage.prototype.setItem;
  Storage.prototype.setItem = function(key, value) {
      const event = new Event('local-storage-changed');
      event.key = key;
      event.oldValue = localStorage.getItem(key);
      event.newValue = value;
      window.dispatchEvent(event);

      return originalSetItem.apply(this, arguments);
  };
})();

var isShowError = false;
function showNetworkError(){
  if(/mobile/i.test(navigator.userAgent)){
    return;
  }
  if(isShowError) return;
  var arr = [];
  var dom = document.createElement("div");
  arr.push("<div id='showErrorWrp'>");
  arr.push("  <div class=\'ant-modal-root animated fadeInUp\'>");
  arr.push("    <div class=\'ant-modal-mask\'></div>");
  arr.push("    <div");
  arr.push("      tabindex=\'-1\'");
  arr.push("      class=\'ant-modal-wrap ant-modal-confirm-centered ant-modal-centered\'");
  arr.push("    >");
  arr.push("      <div");
  arr.push("        role=\'dialog\'");
  arr.push("        aria-modal=\'true\'");
  arr.push("        class=\'ant-modal ant-modal-confirm ant-modal-confirm-warning\'");
  arr.push("        style=\'width: 500px\'");
  arr.push("      >");
  arr.push("        <div");
  arr.push("          tabindex=\'0\'");
  arr.push("          aria-hidden=\'true\'");
  arr.push("          style=\'width: 0px; height: 0px; overflow: hidden; outline: none\'");
  arr.push("        ></div>");
  arr.push("        <div class=\'ant-modal-content\'>");
  arr.push("          <div class=\'ant-modal-body\'>");
  arr.push("            <div class=\'ant-modal-confirm-body-wrapper\'>");
  arr.push("              <div class=\'ant-modal-confirm-body\'>");
  arr.push("                <span");
  arr.push("                  role=\'img\'");
  arr.push("                  aria-label=\'exclamation-circle\'");
  arr.push("                  class=\'anticon anticon-exclamation-circle\'");
  arr.push("                  ><svg");
  arr.push("                    viewBox=\'64 64 896 896\'");
  arr.push("                    focusable=\'false\'");
  arr.push("                    data-icon=\'exclamation-circle\'");
  arr.push("                    width=\'1em\'");
  arr.push("                    height=\'1em\'");
  arr.push("                    fill=\'currentColor\'");
  arr.push("                    aria-hidden=\'true\'");
  arr.push("                  >");
  arr.push("                    <path");
  arr.push("                      d=\'M512 64C264.6 64 64 264.6 64 512s200.6 448 448 448 448-200.6 448-448S759.4 64 512 64zm0 820c-205.4 0-372-166.6-372-372s166.6-372 372-372 372 166.6 372 372-166.6 372-372 372z\'");
  arr.push("                    ></path>");
  arr.push("                    <path");
  arr.push("                      d=\'M464 688a48 48 0 1096 0 48 48 0 10-96 0zm24-112h48c4.4 0 8-3.6 8-8V296c0-4.4-3.6-8-8-8h-48c-4.4 0-8 3.6-8 8v272c0 4.4 3.6 8 8 8z\'");
  arr.push("                    ></path></svg></span");
  arr.push("                ><span class=\'ant-modal-confirm-title\'");
  arr.push("                  >您的网络好像有问题！</span");
  arr.push("                >");
  arr.push("                <div class=\'ant-modal-confirm-content\'>");
  arr.push("                  很抱歉，我们无法加载所需的资源。请检查您的网络连接并重新加载页面。");
  arr.push("                </div>");
  arr.push("              </div>");
  arr.push("              <div class=\'ant-modal-confirm-btns\'>");
  arr.push("                <button type=\'button\' id='showErrorButtonCancel' class=\'ant-btn ant-btn-default\'>");
  arr.push("                  继续访问");
  arr.push("                </button>");
  arr.push("                <button type=\'button\' id='showErrorButtonOk' class=\'ant-btn ant-btn-primary\'>");
  arr.push("                  刷新重试");
  arr.push("                </button>");
  arr.push("              </div>");
  arr.push("            </div>");
  arr.push("          </div>");
  arr.push("        </div>");
  arr.push("        <div");
  arr.push("          tabindex=\'0\'");
  arr.push("          aria-hidden=\'true\'");
  arr.push("          style=\'width: 0px; height: 0px; overflow: hidden; outline: none\'");
  arr.push("        ></div>");
  arr.push("      </div>");
  arr.push("    </div>");
  arr.push("  </div>");
  arr.push("</div>");
  arr.push("");
  dom.innerHTML = arr.join("")
  document.body.appendChild(dom)
  dom.onclick = function(e){
    if(e.target.id === 'showErrorButtonOk'){
      location.reload()
    }else if(e.target.id === 'showErrorButtonCancel'){
      isShowError = false;
      document.body.removeChild(dom)
    }
  }
}

try {
  new PerformanceObserver((entryList) => {
    entryList.getEntries().forEach((entry) => {
      if(entry.transferSize === 0 && entry.initiatorType === "script" && entry.decodedBodySize === 0 && entry.name.indexOf(".educoder.net") > -1){
        // showNetworkError()
      }
    })
  }).observe({ entryTypes: ['resource'] })
} catch (error) {
}



try {
  window.addEventListener('error', function(event) {
    const target = event.target || event.srcElement;
    if (target.tagName === 'SCRIPT') {
      showTip()
      console.error('JS文件加载失败: ' + target.src);
    }
  });
  
} catch (error) {
  
}

// // 创建一个 script 标签
// var script = document.createElement('script');

// // 设置要加载的 JavaScript 文件的 URL
// script.src = 'https://www.baidu.com/js/1.js';

// // 定义 script 标签加载完成后的回调函数
// script.onload = function() {
//   console.log('JavaScript 文件加载完成');
// };

// // 定义 script 标签加载失败时的回调函数
// script.onerror = function() {
//   console.error('JavaScript 文件加载失败');
// };

// // 将 script 标签添加到页面中，触发 JavaScript 文件的加载
// document.head.appendChild(script);
function showTip(){
  if(document.getElementById("showNetworkTip")) return;
  if(document.getElementById("root").innerHTML !== "" ) return;
  var dom = document.createElement("div")
  dom.id = "showNetworkTip"
  var strVar="";
  strVar += "<div class=\"popup-network-wrp\">";
  strVar += "        <div class=\"info\">";
  strVar += "            <div class=\"title\">提示<\/div>";
  strVar += "            <div class='tc'>";
  strVar += "                <div style='font-size:80;color:#d33a27'>";
  strVar += "                    <img width=\"80px\" src=\"data:image\/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgAAADICAYAAACtWK6eAAAAAXNSR0IArs4c6QAAIABJREFUeF7tXQuYHFWV\/k\/1TCZIEFz58AFigExXZ9LVE0gw6eoBAwgiAkEerk8UX4CorCBEXXDFVSAo4AqrgCjorrDKU1EEfBDJdHUCBJiuTtLVCRIVUBQkkACZZLrOfrczE2Ym86i6daunu6vu9+VLYO757zn\/rX9uPe49hxC30Bh4dNHMPdq3ts90mWYyaKYG3guEGQzspoFmDP0bjBkAdgO2\/wzgaQA20fY\/LzKwSfw3ePv\/E\/8W\/481PE1gBwm3bDzw+F9CCyTCwBTh2JWEXsp17eu6AwcJASQ0vJUZM4Edf\/ZQMog3kJcZ7BDgACiLv4ld55WtLzvzVz39sjeIuNdoBmKB+Lwm+hbM2oe0RJaAw0E4AkCnT4i6d2emldDcu7nK93avWLey7g408YCxQCaZvCFBAJQF8dsJOKiJ51u4\/ncC3w3G\/QnWHpi9oryhyeMJ1f1YIGPQWzpkVhe7bSeA+VgA2VBnYOrBewF+gFh7IF0o3zv17jSWB7FABufj4UXJPTu20WICFjPjuMaapjp5Q8gT802u1nFzptd+vk6jNvQwkReInU0dC\/AJRFjMwJ4NPVv1c+5JMG6uonrz3ML6R+s3bOONFEmBFHtS88h1T6ytFqA5jTctjeMRA7eA+aZMoXJn43hVP08iJZBiNmUQ8RkAzgQQqdgVXFIPuoybQQM3dVuP\/10BXlNAROIi6cvpegI4g7kmjI6mmJnGdfKvDPygDdr1XdbaPzWum2o8a2mBlA\/V99u2DWeCIFYN8aU6booYIOA5Vwilql3ftXLtOkWwDQfTkgIR3y60REKIQvx5fcOx3loObWbC9czaD7qttaXWCq0F78PtnH4eMT7PwJtabbIaPJ6tBFxfdbUfdK9Y+0iD++rZvZZZQcTrWiI+n4FDPEcfdwyFAQKWgtsuSxdW\/zOUAeoI2vQCKfZ07g+XlhDoU3XkLehQLwB4EUDtb2K8wIQXCfwCmF4Aod9l3oOIdgdjD9T+5j1A2J0ZexBh96AOhG1PRGsAXJbOl38U9lhh4je1QOysfjYIS9CQt1O8npjWMfF6QFvH5K7X3PZ16cLq9SomdM2hszurAwNJIq2TGUkQdYI5CWBfFfgKMW4j8NK0VXlIIWbdoJpSILaZOopQu50Su2mnurkALAYsjZCH27ZGlQhkAlv3rlkdr2xsSxK5c8FkQoMJRkYGS6HNViIsRXvisvSyNZsV4oYO1VQC4VOQKD2VXArQuaEzM94AhL8BVGDmleTSyk20eaVZePKVKfPHw8DrzQP2eonaFmrMCxi0AIBJwC4eTFV36SOmpelC+WbVwGHhNY1ASmbyYAYtBXBYWGSMj8vrCXSbC\/r5bVZ55UWAWDWatomTjolt00wCHU+M4+v+xo9xTbuG81N5R5yObOjWFAIpZlOnE\/GlAOp2Qo8A1wVu18C3benf7bb5q1Zta+iZlHTu4Xn7797R0XY8QIsBHA+gXRLKpxmt0Mg9f06+stynYV27N7RAxG+6tm0dl4Jxeh1ZeQREt7Hr3p4pVMp1HHfKh6q9EWRaTC4dD8KisB0iYAuBzp9jla8KeyxZ\/IYVSCmnH8YMcUt1sGxwPuyeJdAdLrl3ZvKVu33YtWzXUjb5NmjaqS7zaQS8JuRAf9g2QOfPfrD8XMjj+IZvSIHYZupcBi8lIOE7In8GdzP4zo5puENfVnnWn2k0ej\/Wk0omGB8F+DQw3hhi1I9ozOfPKVR+F+IYvqEbTiDFbPIqIvqM70i8G\/QBfAdpuDPdW+nzbhbtnqWDu97IbQOnEdFHGRDfW5Q38dw3eMt1uXJwScCGEoht6rcCOEkylgnNCLi\/Svh2d975RRj4UcF8et681zw7bdNpGmkfZ\/CB4cTNVxlW5XPhYPtDbRiB2KZuhZEggYEKEa408s41\/qiJe0\/EgBDKP6e9tISJxU4G5WdsGLg1YzmnTPUsTLlA1r4t9fpqGz\/M25OtqWybGfi2xm1XtsKmOZXEqMSyc\/p8Yixh4GSVuINYvYblTOnm0ykVSO0B0GUbgEi1qbL9kJm+nSmUBXbc6sBAKZs8laEtAXGX4uEeH5jWP\/\/AZRs2Ksb1BDdlAimaqSyBxW2VylYgpovi\/E4qKfWOVewxXqe5W8VqIm671DXCS0xuJtO77o\/qQL0hTYlABlcOkUNWXWP8YLpbPadz5XqxjTxuU8jA6lzyXS6T+Ph3gFI3CAcbeedhpZiTgNVdIOWcvts2rp2FUNaI+Jx0vnKlMsAYKDADg8eefwDgqMBgwwHqLJK6C8TO6X9V9cGJwKvB2rnxLZXSS1ApmG3q3x1Ms6QMt70N+6cecJ5QBjgBUF0FYpv67xXuxr3FrVbP6V65\/sl6EBWPIc9AMaufQoSfySPsbFll3mduofKUSsyxsOomkFIudTUzn6UiIBd8UbdV+aoKrBijPgyUurqm8R7VtQD2VzMiPbMtMWActHz9P9TgjY1SF4EUTX0JAWK7euDG4HMzVuWKwEAxwJQwoPKDMAEPdVSr7wjzxUzoAimZyU8y6DoVs+ESf7I7X7leBVaMMXUM2Kb+GIBuNR7Qr2ZM23Lyfss2bFGDNxIlVIH0mfrJmkh+HLy9ApeOMVaUlwWHihEagQHbTK4DaJYSX4juuDVfPjmMk56hCWS1qR\/uAgq2LtMzbQOY04hnBZRMboRBiqb+FAFvVkEBAd9NW46SZ9zh\/oQikJLZORfQ7mTgrQGD7zMsZ25AjNi8gRmwTV0U6lFylDqMW3DlAuFFi9pKW\/\/2O4APDTIvDDySsZx5QTBi2+ZgwDb1rSrOwouE2lVXO0pl6lPlAimZ+qUK9uL8xbCcRkuA1hxXW5N6aZu6SJ00Paj7zPTbTKF8ZFCcIXulAimauqjxF7QS0Zb+fn7L\/FXxEVhVk9wsOKp2WTBwWcZylGyYVCaQPvOAvTS0iYfydJAJIbgHpq114jVg3CLIgG3q4mNiKnDoxO8z8pWfBsVRJpCimbqOwJ8M4hBpdHy6t3xXEIzYtvkZsM1kAaCFASP5U6KqHRm0uI8SgRRNXaSG+WGQgAh8VtqqiI1tcYsZQCmn\/5oZRweigugOI18+MQhGYIEUs8kUiH4X6H0245tGwTk\/SCCtYGsvSL8B07a9gau0K1cH\/hL1jZi2qYsNjsHOpRM+YeQdse1eqgUWSMnUbwlyHpmBuzOW824p71vAqC+ben+C3KMY9NFxwrlFvJnRkLg1imfrFYhknYuBHtnKvIEEUjKT5zBIOocRARuqhKO7847a04VNIJySmToJ4LO9VsQSZ19coksyeecnTRCeUheDioRAV6StslRFAGmBDJ4pF2+t5NPoa3yi0Vu5QymbDQ7Wl0seojGdHSD\/1y2G5by3wcNU6t66BbNeu0VL\/B4E+Q\/HLh0ms5cvgEB08dxxuCwTBFyYtpyvy9o3o10xm\/wiEV2iwnfDcqTnTsX49cYoZfWjmfBr2XGJ8et0wTnGr70UyaVs8iwmutrvYEP9CXxz2qp8QNa+Ge1qaXGIVNbri9xKUsrpX2PGhQHm\/9OG5XzPj71vgQwmXVgJYLafgXb0JRQTmnZ01\/K1f5Wyb0IjO5syQXwvgBlq3efLDavyBbWYjY1WMvXfBii99ye3Wu3x83bQt0CC3iZoTB+cUyjf1NjToM670qKuGby1KsRhqkN9Fcllfkd3g2VEDyPOIUzx7AuwuL2XevZl5qszhcpnvfroSyCP5PQ3tzPE6rGP1wFG9bvNsJwwUlRKuhO+WdBfKJN5SMBt6YhxGvTtaZV5wdxC5cHJuBU\/9yUQ20x+A6AvewEe3Uektq+SluvOr10hY9+sNrapFwEYYfo\/PVHdqzPk5AVh+i+DHbASwA2G5XzMy7ieBdKX03Vt++ohV8Q+gl\/LayXktnaIA0GhNmZ+T6ZQCbqLOlQfVYPbCztnQ9PEZ4Y3yWBTAj3p5U5+MlvPArHN5HcA8nzvNnJgWu9iW072a+ZkQTTqz8XJSob2aNj+uYzPdxecb4c9TqPh21n94yBIJfFg4CcZy\/nQZDF5EsjantS8AZfFPZs2GeCYPyecGcX6HKLOHxOJVTfUJg6oZSznslAHaVDwUk7\/BTOOk3GPCIen8879E9l6EkjRTN5A4+8Vmsy3+wzLeedknVrx56JqLLna46HHxvi4UXAC7aYO3ceQBrCzqWNBLHVEgoFbMpPsSphUIHZPahFcnlBlkyjwlLTliNJqkWu1LRKJxAthB06ExekIl5YLtIowHT1RbudJBRJwt65tWE4m7AukkfFtU+8PoUDQyJCZckahrLrWSiPTOsK3IKsIgDsNy3nPeMFOKJBiLnkMMf1Klqko7rcazZVt6k\/LvmnxynubRvrs3nLFa\/9W7BdkFWHid2fylbvH4mVCgdim\/n0An5AkdFubRumoT1w9voO8PEB7Lniw\/JzkPLWEWcBV5HrDcsY8Lj6uQAaTMKwB8HpJBn9ieHiNJondNGaKSz6MFTcbliP3drFpWPTmaIBV5DkXA11jfYYYVyDFbOp0IpYunewSFsc1yYGgh308XBr\/MCxnLw\/9Wr5LkFWEmc7IFMrXjiZpXIHYpn4PALnXs4xVRsGZ3\/Iz4iFAO6t\/D4QzPHSV7bLWsBzVlWVlfZlyuwCryL2G5eyUJGJMgRR7UvPIZeliiVH+cDX6Cima+n8ScEGIV86U1xIPMTbf0IFWEY3mZ3rLq4YPOqZAbFMXJ\/3+3bd3gwaJqpYMmo9IduxGs7Oz+tkghLkNZMLXlI3GRz38CZBX6xuG5Yz4ZTa2QLKp1fIF4fk3hlVRW9m0HqyGNEYxp3+QGP8bEryAHfcNTIhjNjR0ydQvYOA\/fTvJtMYolOdMuIIUs8kTiChAIgX6gmGVpTOd+A6qwQ1K2dQ7mVg8z4XSiOjSdL78pVDAmxR0dbbrQJeqj8i4P3pn9E4riJ1L\/QjMp8qACxsXmtFtrS3J2reanZ3T54PxUHhxxb+QxuLWNnVRjeztvnkn+rGRL39kyG5ngZi6KKu8t2\/g7QZ\/MCxnkaRtS5o9ujA1s03j0Gp6M+i0jFW+sSXJCxCUndPPA8P3DmdmPJEpODsq8Y4QyBpz9qFVuH+Q9ovoy0a+rCStjbQPDWY4eCZ9U2huMR1nFMq\/DA2\/SYFLh8zq4mpitYz7w7fujBCInUt9GczfkAGt3V652jyV1X1k\/Wg0u5Kpv8IKisOMFZdLWjZqx5i9zq9t6iJZhu8XRgR+f9qq\/J8YZ6RATF1sTPSdXEsAMXhFxqpkvTofpX5FU3+S5G9bJ6SKuK0zXVi9Pkp8eo3VNvXPAfgvr\/2H+g0vwDNaIBtlz5y74Iu6rcpX\/ToThf5q64KPZGzGtF1et9+yx8S8xW0UAwFus3Yc8tshEHthahE0+YNR7LpHZlas+208SzszEDDZ2biUMriasSptMefjMyCzm5qBf2QG97ftEEifmfyqBvoPSbL\/nracN4nUPpL2LW1mm7q4n\/1X9UHyM4ZVeaN63NZBtHP6NWCc7jeioQf1V1eQrH4\/CHKvaBVU8vEbQDP1L+VSVzNzCEXueXXaqgSqCdlMPMr4apvJDwP0Y7+2xPSBdKF886sCMfUBAAm\/QKI\/g8\/NWJUrZGyjYFPMpi4i4q+EEGv83WkSUmtVuxLb\/uaf++15j2sCCfr8QYnqnPTy9eJwVdzGYMA2U58F+DvKyWG+3ShUTlKO22KAtqmLnQz+jl8wlhkF57CaQIpm6qMEvkGSl8cNy5klaRsJs1I29X4mVp6wm8HXZayK7\/vrSJA+LEg7p38TDF9Z8BmoZCxHrwkkyAM6ga9Lx5M04TVn52YfCXbvU39h8sWGVZE+lqDen8ZEtM3UUUCt\/ISftsmwnNfWBFIykzdMUERyYlByTzLy6273M3LU+gbZXToRV0R8TjpfuTJqfMrEa5s6+7VrJ2wXiB3gDdZWHuicV3g8\/pI7AfulXNe+zNU\/+Z2gyfvzqYZV+Z\/J+8U9SqbuMJD0w4R41Tu4guhPMDDTj\/FgX9ewHKk3XxJjNa3Jw\/PmvaajY\/NLqgOYKJ+T6rGaHc82U78E2F+5cZe2P6TLLD\/CjgEnYzmpZievHv7bpi4E8hqVYxHzgrTHQjAqx21GrFI2dQUTf96P7+JbCAU6r0C4y8g7x\/sZNKp9bVMXt1j7qoyfNfeATO+6P6rEbFUs29TPBPBdf\/HRFyjINxAGXZGRLNDuz9Hm7100k48Q6ECVkUyvVnfvXLn+RZWYrYq1Ops8wiXyuVeQL6cg30DGS7bVqiQHics2k\/cBdGQQjFG2Ww3L6VCI19JQMi9KGHwzBfkG4gJHdFvO71uaWUXBFc3kTQR6vyI4AfNXw3LerBCv5aF8H1xjLCPbTH4LoHNl2HGr1bf4qTktM0ar2AQrYTcmC5EvLeH32pDY+l6gkpm8lkGf8jsYgAHDctol7CJpUsolv8JMFykM\/n7Dcg5XiNfyUL7T6TJWUYCl\/znDcvZseVYVBVjMJs8ioqsVwQmYW4xJyocpHKsloOys\/lMQ3usjGJsCJPuNNyn6YTqX\/Fcw1RIBKGmMa4yCI15dxs0jA0UzdR2Bx6wDMhaE2LBIsttMGHgkYznzPPoW+W592eQRmu\/XjOPTxsDXM5ZzYeSJ9UGA3129BGwQAnkYBN8XOgG\/T1vOET78i3TXUk+ym116TBkJjH8zCo7vjB3Kxm9CoKKpX0A+cvYS8DQVTd0hn5u4atzEx2x9XSJONrn3ViKRtVJJY8KHMnnnJ0rAIgIicXDtOSqZ+lMMyLxPv8GwnI9FhNvAYd6\/aOb0Pbd2vBIYaBCAJilfrGqcVsIpZZOnMtGPfMS0iWxTF1sVdvNhVOvKhG9n8o6vzV9+x2i1\/rapixSkM5TERTjYyDvSRY6U+NBkIEVTX0zAnT7c3ioE4vsgiRggThTng+bBriVT+ljBToMNuLTfgSvKG\/x7EV0LmX2H0isIgO8ZlvPp6NLtP3Kp5AHjDEPTErull63Z7N+L6FpIrCCbgjyD3GZYzsnRpdt\/5L6\/5I4nDmBL2nJ28e9BtC38PoMEeotFwPK05Rwabcr9RW+buijF9kF\/Vjv3ZuCpjOXsExQnavZ+32INfSiU+g4CwDHi04S+rjHb1EUxz7N9GY3duc+wnLkKcCIF4fc7CMReLNkv6QCeNyznXyLFcMBgpYtLjhqXgN+lLecdAd2JnLnfL+kQ290D7MUCbUx0pNes2Ro5piUDtnP6GWB8T9J8uNlPDct5nwKcSEH43YtFhLuC7OZFfB7E3\/VVzOqnEOFn\/qx27k1E\/53Olz8TFCdq9n5389ZOFAY4DxKXXPN5hZVy+mHMCHwCk5m+limUZUtV+PS6dbr7fYsosoYGOlFIjHelC05oNcBbZ2q2R1LMpgwiLgaPiz5nWOWrguNEC6Fo6jYBPspF8OWBzqQT80fShYrv2gvRmpZXo11zyOw3Vavu00HjH6pdERQnava2qfcDmOY1brFbJFBWEzCdZxTK3\/I6YNT78bx57aWOzcFfapB2lJFf+5uo8+kn\/r636ftpbfCVQ0zUoA+UFwuEa428c4YfR6Pe1zb1FwC8NggPGicOmlNY82gQjKjZlrKpdzKxv8cBkXo0UGZF4CHDct4WNbKDxGub+uMA9g+CQZR4azq\/5s9BMKJmK1MSWmwIDZSbF0B17d5Ox3tvQTVqhMvGa5v6SgCBfqn098\/Ydf6qVS\/L+hBFO9vU\/xuAr821huVQ0Ozu8aten1dboFos28cqG5Yz2+ewke9u51K\/AbPn3QfiPHracgZXkAD1QRj4WMZyZMu3RW7igqR6FWQR0aXpfPlLkSMuYMC2qYtb0rd4hhleozDIbzVivjpdqHzW88AR7xj0W0hCc4\/s6l3nMwlztEl\/YtHcPTZvfeV5PywQ+Ma0VTktcI1CAJZhOTk\/g0e9r\/TJQsLf6PnEW+P9b\/6uIDuXOhHMt\/mxGjoxq6LK7ZYZ0\/pft9+yDVv8OBDlvnZWvwyE8\/xywExXZgrlc\/zaRb1\/MZe6mpjP8sOD+AaSsco3KqmTrmnuwjm968Tbmbh5ZEBiF3XBsBzTI3zcbRgDdja1GsRdvkhx6TBjRXlZTSCiFc3kAIGk6g3GdUJ8UV\/r3GcesJeGtl8DOMiLNWs0P9NbXuWlb9znVQZK2QNmMbWt88MJg6sZq9ImbHYIxDZ1UUf6KD9Aw\/rGCRwkiCt1dU3j3d3PgVicMhzzCC0Dv9BcXJZe4eQlhoi8SdHUxYP2D30ScZ9hOe8cKZCcfh4Yl\/kEqnUnopXpfHmhjG1sA9gL0m+ANvBhgD9MGt4siqOCa3\/yRsHxO7kxpcNvr0xd8HeaL1II5xt555sjBFI0U1kCW76AhneOE5lJUxcbhseAberrARzgZwQGmRmrXBghEPEftqn\/E8Dr\/IAN9WXgwozlfF3GNraJGQiDgb7c7IUau7UL3UcbkWthxzOIACiZ+i0MyOa66jUs5xAfjsRdYwZCZUCm\/iYBt6Yt55Qhx0YKJJf8PDNdIes1aTw33Vvpk7WP7WIGVDJQyqVWMPMCP5hEfE46X7lyTIEEfQ5h4IsZy1nqx6G4b8xAGAz0LexcoGnaCr\/Yw58\/dnoGGXwO+ct4rxw9DBYXlvRAUtwlfAZkbq8APGlYzogNjSNusYTbxWzyRiL6iGwIlKjOSS9fv0bWPraLGVDBgNztFf04nS+PuPZ3EoidTR0L4rtknRx9DyeLE9vFDMgyIHt7BaKTjHz59uHj7iSQ2ipi6qvI4xaIMYLY8RVSNsDYLmYgCAMyt1cErFuztzN79OnYMQVSyia\/yESXyDqZqGrJrpVrfe1\/kR2r2e3W9HS+Y5tLSSLqJFAaDKMWE8FmcImZ17VrXInPgHifaZnbKxC+ZeSdnXZYjyOQObOYBlb7ySE03P24+pS3ybRN\/X8AfMhL7zjdqBeWANlHBEqgJ7185\/1uYwpEuFLK6T9hxge8uTWylyg8oiW0+V3L1\/5Vxr7VbQZz9ApxdPiJlYCKltAWxbyOz5rEMQKxY7c3Pc5H7nEFYmeT7wHRiAcWP5MJ8FcNq3KRP5vW721nUyaIA+3MbSfsnco7gTM0thrbsqvHRAkQxxWIIM82dZFHdvs9sc8WryJjE6ai0i0znsgUnEC5tXxOZ1N0l1k9ROqqgao2+8BxnpknFEgxl\/wKMUmvAvGzyMjrys6lLgazkowkDHw9YzkXNsWVWwcn5VcPvt0oVE4az8UJBVI6ZFYXVxPiYV2qxavIq7TZuc4jwdp9UkSOb\/Rhw3JE3cPIN8nVAwCfalgV8Tw4ZptQIMKiZOrfZ+ATsjMQryLbmSvmkp8hJqUlCxh8Y8aq+DsMJDuRDWwnvXoAj91qOfMuAlxpgUjuqd8xnlhFqm3o6X7AeaKBOQ7dtVI2eRUTKa0KxUwrM4X4JKfs6kHMn0kXKiIl6bht0hVEWNq51I\/AfKrsVSRKWWWsitQrY9kxG83ONpP3AXSkSr8IeDFtOburxGw2LOm6j0xrnu3YMu+wSdJVeRJI0ew8lKD9ISB5ZxuW852AGE1rbpvJPwPkPfWlx0hZcw\/I9K7zVffCI3TDd1u7MDVzQOMHfKUUHYyKwedmrMqkZ588CaS2ipipmwEOUll1MzEfkS5UHmx45kNw0M7qm0HYVTl0hHMBlMzUdQz+pH9Oaf306sC8zpXrX5zM1rNAVmeTR7hEwXLCEv3WyJeV3mZMFmCj\/LxoJgsEUp75haYldksvW7O5UeKslx99ZuokDXyrzHjM\/KVMoXKpF1vPAqmtItnkbSA60QvweH2i+v7eb41uLxyLrSdpy9G99G2lPn2ZzK40o\/8BmR3nBPy5fRrP05dVnvXCiS+BrM4l3+Uy3e0FeOI+7rGGte5XwXGaB8E2U+cCrLaeI9EdRr4c6BdW8zD4qqe2mfwGQF+W8Z2J\/yOTr3zNq60vgQjQkqnfycBirwOM2Y9QTGja0VHadNeX04\/XGD8PxNsoYwYuy1jOEpWYjY4lVWvw1aDKbQPUM\/vB8nNe4\/QtkMHTWuLNgedyumM5Q+Cb0xF79WsHKFQ0BocvUqKajdLx5r4Fs\/ZJJLR7GDTH6wU+vB+BTktb5Rv92PoWiACXObE1tkhwYTpCyeZKZvJgBil6ixe93dK2qf8MwI6cVb4udKbb04XyuHuuxsOSEohIuozXuQ\/4zTk0phMan2j0Vu7wE2wz95WtDTLyNyGvTluVdDPz4Nf3IL+UGRhg1+3pXuG\/RIeUQERwRVNfTMCdfgMd3V8US6wSju7OO05QrGaxt3P6N8H4gqS\/kTvzP3jATKwesu0bhuVcIGMsLRAxmJ3TrwHjdJmBh9swcHfGct4dFKeZ7Es9yW52SWTT91hygp8B40yjEJ3VVszn4I5yUZpjzPIQk845odgO9KTyzqZJ+47RIZBASrmufZmrywHsKzP4CBvGN42Cc35gnCYD2H7rAJ0BnUDim8ZrhkJg4I8EXgOmtVXwf80tVJ5qsvACu2ub+j0AarU6ZJqm0Qfn9JZvkrEVNoEEIgCK2dTpRHyNrAMj7Ij+3ciXL1aC1aQgtjnrgCprr5+u0VrZ33pNGvpObtu51O1gfk+AeH5qWE6Q7VHBBVK71cqmbgcFCmQHBxroC3Os8uUBSIlNW4AB29TFh+RjZENh4Cmq4hhjpSOOjUu3wCuIGFnsqtym8b0EJKU9GWZI4LPSVuW7KrBijOZjoGjqvyXgiECej5ElUQZPiUBqq4g5+92A+0sZJ8ay0YiPmZOviCKXcYsQA7api2faniAhE7AkbTlS5QRHj6tMIAK4ZOrnM6Cu\/IHrdhkr1q0NQlZs2zwMlEx9JQNvC+jx9YblSGyBH3tUpQKprSQBTx+OdnPGtP5d9pvk1FdAQmPzBmDANnVRHzMbyBXGsj3w0jFvKTz5SiCcEbf7qpAGcZxFyT239tM9IMxTBF3t75+xy\/xVq7YpwothGoyBkqkv54C3VaoeykNuGtdZAAAHUklEQVS9xRoCL2ZTb9eI72FguqK52PQCYe8eyY89inyIYUJgoGSmHmHwgYGhFT2U10Ug259Hkp9m0IQZI\/yRws\/09yM9f5W3gy7+sOPe9WagzzxgrwTaxMoR+M2nyofyugmk9jxiJr8F0LkKyf9jlfnQKH5RVsjhlEOtMWenq3DFF\/K9gzvDVxlW5XPBccZGUP6QPnqYopm8iUDvVxUAAyVo7uKoZvJQxeNU4fRlk0doROIs+R5BfWDg1sywks1B8cayD10gtZVE7UEhAfkYXPcD8SvgMC6J8DCLOf2DxLgBQLuCUXqNcUoWKMDeAVEXgYjRiqbuqPrSPuj94y7hnO688wuVhMRY4TAQNBH6KK8eNyxnVjiejkStm0DWLZj12i2JxJMAdlMZGDF\/Ke0xhYvKcWMsbwzU5r2t7aogmTlHjER4aaC9f58Dl23Y6M2DYL3qJhDhZt+h+n7aAMLIAvi\/0xPVczqXr\/9HMDpia5UMPJaddWACiatAyKnCrXcmyboKpPY8ktPng\/GQKsKG4TwCl841VpSXhYAdQ\/pkoM\/UT9YYQhxv9Gk6fvcpyCJZd4GI6NUmLxjBZz+Bz4l3Aiu7JKWAiqa+hABPmQu9DqBx9aA5hfWPeu2vqt+UCEQ4X+zp3J9c7XFVgQzHEdnkNQ1L072VvjDwY8yxGVjd07nAdekCgI5VyNHLiYQ2a6pyqE2ZQGrPJAtm7aMlEo8BeL1CQoegNhOwdEv\/jKXxPq4Q2B0FWTL1CxgQiRF8Ve6dyDMGP6pNazt0KnMPT6lAaiIxD9hLQ5s4lD83pGl8EERLjXw5QMXekDxrAVg7N\/tIMF8A8KFqw+H\/M6yKsg\/Msr5NuUCE44\/P23\/3lzvaxcF66SOWkxFA4BtdxtJMoVKerG\/888kZWLFg1mt31RIXgHDe5L399SDgu2nLOcufVTi9G0IgIrS\/ZPfZZSPtKkRyQjih1lCfc8FXtycS107VPW2IsdUNuphNfYqIzwxj1W+0fMMNIxAxu3wKEqWndLEV4cNhznatbiL4+7FQ\/LFcEwb4UwrP+uxwgIEqgZYYDZawo6EEMsRW0UyeQ6glVUv4m0J\/vWOheOMrTGEMevAQEZak88793jyqX6+GFIgIf7WpH+4yLgvjt9VoemOhjH3B1UEYIjPbtQPt\/V+s19YRv9JqWIGIQErZOf\/CGBAi+bjfwCT7\/5PB92qMe9iddq+xsvSMJE7TmonToETucQw6TvHm0tGcbGSmL2YK5WsbmayGFsiOW65c6jPELG65dqkjmZsYuJcI97gD1Xu7V64XGy1bsok9U5qmHUdMxwGYX4cg7yfwkrRVCWPLkVL3m0IgtdUkl+xhrj2XBMt8IUEfAVsA3CsE47bhnu4HnCckYBrKRCSFrg4kjiKCWCkOr59zfHl678oSugXV+o0pP1LTCKQmkkVdM7i\/+pUw3r37oFBM7L1gur8K2Am37bFmuBWzF6TfgMTWo0CUA8MEYPiIOXBXApa7xJdkmiwZYFMJZGiW+nLJQzTG+Yr3\/EhfBAxeTYAN0opgrGKt\/aFMr\/28NKAiQ9tMHQXiI8FYVKdbp7E83wyii418+RJFYdUVpikFMsRQKZv6FBOLIpb715U1D4OJEs0MPAqmh8F4mNrc5zU3sfHl\/v6N81f98QUPEJ66lLJzZrnatk5ibRbgdhLTLCbuBKguJ+4mcfKWKlcvmTsFu3A9keehU1MLRMS35pDZb6pWXSGSsz3E2yhdGIBYYTYyIE7GbdSA55mxEcQbibWXXOLpJDb+EaYTcwdDmw5CB7s8XRN\/g\/dtEBGMwSmvZ9DFGcsRH32bujW9QHbcdm3PliEK8His2NTU89bAzvNVNK3t4vSyNX9rYCc9u9YyAtlx25VLfoKZTgNqD6JxqxMDBL7O1bTrMr3lVXUasi7DtJxAdgjFTL6PURNKvKKEeCm1qjCGKGtZgQwFaGdTxwLuaSA6McTrJHLQrS6MyAhkh1AWphYhgdOUpZ+JnCS2BxwVYUROIDuEUsuqwu8hYDGD5kT0OvcVNoFXM\/Bz1rTbW+0ZYzIiWv4WayICtt9+8QlEQizYczKyovRzAp5lxs8ButMolJWV1ms2DiMtkKHJenhRcs+ObbS4tqowxIa9yDYi3CVWi\/52\/vn8ZXGpiVggo6QgNvGx23YCs3scgRZGQSkMXkGk3UXawJ3p5evXRCFmrzHGApmAqUcXpma2adwDRo4JPQSkvRLbyP1ECQli9IKQH3Cp98AV5Q2N7O9U+hYLxAf7QjAJDYs0uG9noreDsZ8P86nrSniCmP\/gQvtD1cWyWBDepyIWiHeudupZEwy5cxkkhPNWZswEdvwJXCDGp2tiT5dYCTYQYUPVxZ8IvKHK2mOxIHwyOax7LBB57ia0fHTRzD3at7bPdJlmCgFp4L0Y2E0jzGCQKAEx9GcGAbuJnw0rDbGJAHGicROAzdj+9yYCb3IZImPkJhf0dyEAjXjDtmnbNjTqme6Q6K0b7P8Dx8tG5AMFrQkAAAAASUVORK5CYII=\" alt=\"\">";
  strVar += "                <\/div>";
  strVar += "                <div style=\"color:#666;margin:15px 0\">";
  strVar += "                    <span>资源加载异常<\/span><br\/>";
  strVar += "                    十分抱歉，你当前的网络不稳定，请根据下方提示进行处理。<br\/>";
  strVar += "                <\/div>";
  strVar += "                <button onclick=\"window.top.location.reload()\" class=\"button-reload\" style=\"background-color: #3061d0; cursor: pointer;padding: 10px 25px;border-radius: 2px;color: #FFF;box-shadow: none;border: none;\" size=\"large\" type='primary'>刷新页面<\/button>";
  strVar += "                <div class='tl' style='line-height:4;color:#3061d0;font-weight:bold'>";
  strVar += "                    具体错误信息:";
  strVar += "                <\/div>";
  strVar += "                <pre style='background:#f8f8f8;padding:15px;text-align:left;color:#444;white-space:initial;margin: 0;line-height: 1.8;margin-bottom: 10px;'>";
  strVar += "                    您当前的网络不稳定，请尝试以下解决方法：<br\/>";
  strVar += "                    1. 请检查您的网络是否正常，如访问百度（<a target='_blank' href='https:\/\/www.baidu.com'>https:\/\/www.baidu.com<\/a>）<br\/>";
  strVar += "                    2. 访问其他网络正常，请检查网络是否存在限流，测试地址xxxx<br\/>";
  strVar += "                    3. 尝试使用手机网络访问头歌（<a href='https:\/\/www.educoder.net' target='_blank'>https:\/\/www.educoder.net<\/a>），若手机网络可以访问请继续尝试第3步<br\/>";
  strVar += "                    4. 修改电脑网路DNS地址为8.8.8.8 或114.114.114.114后刷新页面重新访问<br\/>";
  strVar += "                    5. 清除浏览器缓存后再次访问";
  strVar += "                <\/pre>";
  strVar += "            <\/div>";
  strVar += "        <\/div>";
  strVar += "    <\/div>";
  dom.innerHTML = strVar;
  document.body.append(dom);
}

