"use strict";

const stepContent = [
  {
    title: "先让两台机器，<br>认出彼此。",
    description: "在一台机器生成邀请，在另一台复制导入。双方记住对方的设备公钥，建立属于自己的信任关系。",
    note: "邀请 5 分钟内有效，仅能使用一次。配对完成后，还需要单独选择共享的串口。",
    next: "下一步：选择串口"
  },
  {
    title: "共享多少，<br>你来选择。",
    description: "在串口所在的机器上，手动选择要共享的端口。已配对设备只能看到你显式开放的串口。",
    note: "配对与共享，是两次不同的授权。试着关闭右侧共享，再看看下一步会发生什么。",
    next: "下一步：连接工具"
  },
  {
    title: "远端的串口，<br>本机的打开方式。",
    description: "连接已共享的串口，在使用端生成本机映射。现有工具打开这条路径，就能与远端设备收发数据。",
    note: "macOS / Linux 使用 PTY；Windows 使用预先配置的 com0com 端口对。图中路径为演示路径。",
    next: "再看一次连接过程"
  }
];

let currentStep = 0;
const stepTabs = [...document.querySelectorAll("[data-step]")];
const stepPanels = [...document.querySelectorAll(".step-panel")];
const arrowMarkup = '<svg class="icon" aria-hidden="true"><use href="#i-arrow"/></svg>';

function setStep(nextStep, focusTab = false) {
  currentStep = nextStep;
  stepTabs.forEach((tab, index) => {
    const selected = index === nextStep;
    tab.setAttribute("aria-selected", String(selected));
    tab.tabIndex = selected ? 0 : -1;
    tab.classList.toggle("is-active", selected);
    stepPanels[index].hidden = !selected;
  });
  const content = stepContent[nextStep];
  document.getElementById("step-counter").textContent = `0${nextStep + 1}—03`;
  document.getElementById("step-title").innerHTML = content.title;
  document.getElementById("step-description").textContent = content.description;
  document.getElementById("step-note").textContent = content.note;
  document.getElementById("step-next").innerHTML = content.next + arrowMarkup;
  if (focusTab) stepTabs[nextStep].focus();
  if (window.gsap && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    gsap.fromTo(stepPanels[nextStep], { opacity: .4, y: 6 }, { opacity: 1, y: 0, duration: .3, ease: "power2.out", overwrite: true });
  }
}

stepTabs.forEach((tab, index) => {
  tab.addEventListener("click", () => setStep(index));
  tab.addEventListener("keydown", event => {
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % 3;
    else if (event.key === "ArrowLeft") next = (index + 2) % 3;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = 2;
    else return;
    event.preventDefault();
    setStep(next, true);
  });
});
document.getElementById("step-next").addEventListener("click", () => setStep((currentStep + 1) % 3));
document.getElementById("back-to-sharing").addEventListener("click", () => setStep(1, true));

document.getElementById("demo-share").addEventListener("change", event => {
  const shared = event.target.checked;
  document.getElementById("shared-count").textContent = shared ? "1 个已共享" : "0 个已共享";
  document.getElementById("share-status").textContent = shared ? "共享中" : "未共享";
  document.getElementById("sharing-feedback").textContent = shared
    ? "只有选中的串口，会出现在已配对机器上。试着切换共享，再看下一步。"
    : "尚未选择串口。对端看不到任何可连接端口，即使已经配对。";
  document.getElementById("mapping-available").hidden = !shared;
  document.getElementById("mapping-unavailable").hidden = shared;
});

const pathContent = {
  direct: {
    description: "直连时，串口数据直接在两台设备之间传输，中继不转发这条数据。",
    caption: "DEVICE ↔ DEVICE",
    relay: "随时可以接力的备用路径"
  },
  relay: {
    description: "直连受限时，中继转发端到端加密的数据。只有通信两端可以解密串口内容。",
    caption: "DEVICE ↔ RELAY ↔ DEVICE",
    relay: "只转发密文，不读取串口内容"
  }
};
const networkDemo = document.querySelector(".network-demo");
const pathButtons = [...document.querySelectorAll("button[data-mode]")];
pathButtons.forEach(button => button.addEventListener("click", () => {
  const mode = button.dataset.mode;
  networkDemo.dataset.mode = mode;
  pathButtons.forEach(item => {
    item.classList.toggle("is-active", item === button);
    item.setAttribute("aria-pressed", String(item === button));
  });
  document.getElementById("path-description").textContent = pathContent[mode].description;
  document.getElementById("path-caption").textContent = pathContent[mode].caption;
  document.getElementById("relay-state").textContent = pathContent[mode].relay;
  document.getElementById("network-graph-description").textContent = pathContent[mode].description;
}));

const menuToggle = document.querySelector(".menu-toggle");
const mobileMenu = document.getElementById("mobile-nav");
function closeMenu(returnFocus = false) {
  mobileMenu.hidden = true;
  menuToggle.setAttribute("aria-expanded", "false");
  menuToggle.setAttribute("aria-label", "打开导航");
  if (returnFocus) menuToggle.focus();
}
menuToggle.addEventListener("click", () => {
  const expanded = menuToggle.getAttribute("aria-expanded") === "true";
  mobileMenu.hidden = expanded;
  menuToggle.setAttribute("aria-expanded", String(!expanded));
  menuToggle.setAttribute("aria-label", expanded ? "打开导航" : "关闭导航");
});
mobileMenu.querySelectorAll("a").forEach(link => link.addEventListener("click", () => closeMenu()));
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && !mobileMenu.hidden) closeMenu(true);
});
matchMedia("(min-width: 801px)").addEventListener("change", event => { if (event.matches) closeMenu(); });

// The diagrams describe the product. They never contact devices or create pairings.
if (window.gsap && window.ScrollTrigger) {
  gsap.registerPlugin(ScrollTrigger);
  const media = gsap.matchMedia();
  media.add("(prefers-reduced-motion: no-preference)", () => {
    gsap.from(".hero h1, .hero-intro, .hero-actions", {
      opacity: 0, y: 16, duration: .55, stagger: .07, ease: "power2.out",
      clearProps: "opacity,transform"
    });

    gsap.utils.toArray(".section-heading, .control-heading, .faq-heading, .closing-section>div").forEach(element => {
      gsap.from(element, {
        y: 20, opacity: 0, duration: .65, ease: "power2.out",
        scrollTrigger: { trigger: element, start: "top 92%", once: true }
      });
    });
    gsap.utils.toArray(".feature-lines>div, .responsibilities>div, .control-principles>div").forEach(element => {
      gsap.from(element, {
        x: -12, opacity: 0, duration: .5, ease: "power2.out",
        scrollTrigger: { trigger: element, start: "top 94%", once: true }
      });
    });
    // As the two ends separate, the cable extends: distance changes, the connection remains.
    const bridgeMotion = gsap.timeline({
      scrollTrigger: { trigger: ".hero-bridge", start: "top 98%", end: "bottom 42%", scrub: .7 }
    });
    bridgeMotion.from(".endpoint-remote", { x: () => innerWidth > 540 ? 80 : 14, scale: .82, transformOrigin: "center center", ease: "none" }, 0);
    bridgeMotion.from(".endpoint-local", { x: () => innerWidth > 540 ? -80 : -14, scale: .82, transformOrigin: "center center", ease: "none" }, 0);
    bridgeMotion.from(".bridge-draw", { strokeDashoffset: 1, ease: "none" }, 0);
    gsap.from(".ownership-statement span", {
      color: "#b0b0a7", stagger: .32, ease: "none",
      scrollTrigger: { trigger: ".ownership-statement", start: "top 85%", end: "bottom 50%", scrub: .5 }
    });
  });
}
