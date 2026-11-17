const stepContent = [
  { title: "邀请一次，记住彼此。", description: "每台新设备单独邀请，导入后自动交换公钥，双方互相信任。", note: "5 分钟有效 · 每次配对一台设备", next: "下一步：共享串口" },
  { title: "只开放你选中的串口。", description: "在共享端勾选目标端口，对端便能看到。试试右侧的共享开关。", note: "新接入的设备默认不共享", next: "下一步：本机连接" },
  { title: "工具照旧，串口换个位置。", description: "选择设备和串口，连接后用本机工具打开映射路径。", note: "也可在 Linkport 中直接监听与发送", next: "再看一次：配对机器" },
];

const stepTabs = [...document.querySelectorAll("[data-step]")];
const stepPanels = [...document.querySelectorAll(".step-panel")];
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
let currentStep = 0;

const sceneStage = document.querySelector(".scene-stage");
const serialObject = document.querySelector(".serial-object");
const sceneWires = document.querySelector(".scene-wires");
const cableAnchor = document.querySelector(".terminal-cable-anchor");

function drawSceneCable() {
  const transform = serialObject.getScreenCTM();
  if (!transform) return;
  const origin = serialObject.createSVGPoint();
  origin.x = 150;
  origin.y = 78;
  const start = origin.matrixTransform(transform);
  const stage = sceneStage.getBoundingClientRect();
  const target = cableAnchor.getBoundingClientRect();
  const startX = start.x - stage.left;
  const startY = start.y - stage.top;
  const endX = target.left - stage.left;
  const endY = target.top - stage.top;
  const bend = Math.max(24, (endX - startX) * .55);
  const path = `M${startX} ${startY}C${startX + bend} ${startY} ${endX - bend} ${endY} ${endX} ${endY}`;
  sceneWires.setAttribute("viewBox", `0 0 ${stage.width} ${stage.height}`);
  sceneWires.querySelectorAll("path").forEach(wire => wire.setAttribute("d", path));
  sceneWires.querySelector("animateMotion").setAttribute("path", path);
}

const sceneObserver = new ResizeObserver(drawSceneCable);
sceneObserver.observe(sceneStage);
sceneObserver.observe(document.querySelector(".local-terminal"));
document.fonts.ready.then(drawSceneCable);
drawSceneCable();

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
  document.getElementById("step-title").textContent = content.title;
  document.getElementById("step-description").textContent = content.description;
  document.getElementById("step-note").textContent = content.note;
  document.querySelector("#step-next span").textContent = content.next;
  if (focusTab) stepTabs[nextStep].focus();
  if (window.gsap && !reducedMotion.matches) {
    gsap.fromTo(stepPanels[nextStep], { opacity: .4, y: 5 }, {
      opacity: 1, y: 0, duration: .22, ease: "power2.out", overwrite: true, clearProps: "opacity,transform",
    });
  }
}

stepTabs.forEach((tab, index) => {
  tab.addEventListener("click", () => setStep(index));
  tab.addEventListener("keydown", event => {
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % stepTabs.length;
    else if (event.key === "ArrowLeft") next = (index + stepTabs.length - 1) % stepTabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = stepTabs.length - 1;
    else return;
    event.preventDefault();
    setStep(next, true);
  });
});
document.getElementById("step-next").addEventListener("click", () => setStep((currentStep + 1) % stepTabs.length));
document.getElementById("back-to-sharing").addEventListener("click", () => setStep(1, true));
document.getElementById("demo-share").addEventListener("change", event => {
  const shared = event.target.checked;
  document.getElementById("shared-count").textContent = shared ? "1 个已共享" : "0 个已共享";
  document.getElementById("share-status").textContent = shared ? "共享中" : "未共享";
  document.getElementById("sharing-feedback").textContent = shared ? "未选中的串口，对端不可见。" : "当前没有共享串口，对端无法连接。";
  document.getElementById("mapping-available").hidden = !shared;
  document.getElementById("mapping-unavailable").hidden = shared;
});

const networkDemo = document.querySelector(".network-demo");
const networkFlow = document.querySelector(".network-flow");
const networkPackets = [...document.querySelectorAll(".network-packet")];
const networkToggle = document.querySelector(".network-motion-toggle");
let networkRequested = !reducedMotion.matches;
let networkVisible = false;
let networkAnimation;

function syncNetworkAnimation() {
  if (!networkAnimation) return;
  if (networkRequested && networkVisible) networkAnimation.play();
  else networkAnimation.pause();
  networkToggle.setAttribute("aria-label", networkRequested ? "暂停连接动画" : "播放连接动画");
  networkToggle.setAttribute("aria-pressed", String(networkRequested));
  networkToggle.querySelector("span").textContent = networkRequested ? "暂停" : "播放";
  networkToggle.querySelector("use").setAttribute("href", networkRequested ? "#i-pause" : "#i-play");
}

if (window.gsap) {
  const progress = { value: 0 };
  function drawNetworkPackets() {
    const length = networkFlow.getTotalLength();
    networkPackets.forEach((packet, index) => {
      const position = (progress.value + index * .48) % 1;
      const point = networkFlow.getPointAtLength(length * position);
      packet.setAttribute("cx", point.x);
      packet.setAttribute("cy", point.y);
      packet.style.opacity = Math.min(1, position * 12, (1 - position) * 12);
    });
  }
  networkAnimation = gsap.timeline({ repeat: -1, paused: true, onUpdate: drawNetworkPackets });
  document.querySelectorAll(".network-route").forEach(route => {
    networkAnimation.call(() => {
      document.querySelectorAll("[data-route-label]").forEach(label => {
        label.classList.toggle("is-active", label.dataset.routeLabel === route.dataset.route);
      });
    });
    const color = getComputedStyle(route).stroke;
    networkAnimation.to(networkFlow, { attr: { d: route.getAttribute("d") }, stroke: color, duration: .8, ease: "power2.inOut" });
    networkAnimation.to(networkPackets, { fill: color, duration: .8 }, "<");
    networkAnimation.fromTo(progress, { value: 0 }, { value: 1, duration: 3.2, ease: "none" });
  });
  networkToggle.hidden = false;
  networkToggle.addEventListener("click", () => {
    networkRequested = !networkRequested;
    syncNetworkAnimation();
  });
  new IntersectionObserver(entries => {
    networkVisible = entries[0].isIntersecting;
    syncNetworkAnimation();
  }, { threshold: .1 }).observe(networkDemo);
  reducedMotion.addEventListener("change", () => {
    networkRequested = !reducedMotion.matches;
    syncNetworkAnimation();
  });
  drawNetworkPackets();
  syncNetworkAnimation();
}

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
matchMedia("(min-width: 901px)").addEventListener("change", event => { if (event.matches) closeMenu(); });

if (window.gsap && window.ScrollTrigger) {
  gsap.registerPlugin(ScrollTrigger);
  gsap.matchMedia().add("(prefers-reduced-motion: no-preference)", () => {
    gsap.from(".hero-copy > *", { y: 12, opacity: 0, duration: .45, stagger: .06, ease: "power2.out", clearProps: "opacity,transform" });
    gsap.utils.toArray(".section-heading, .control-copy, .faq-heading").forEach(element => {
      gsap.from(element, {
        y: 16, opacity: 0, duration: .45, ease: "power2.out", clearProps: "opacity,transform",
        scrollTrigger: { trigger: element, start: "top 94%", once: true },
      });
    });
    const cable = gsap.timeline({
      onUpdate: drawSceneCable,
      scrollTrigger: { trigger: ".connection-scene", start: "top 96%", end: "bottom 30%", scrub: .5 },
    });
    cable.from(".hardware", { x: 35, scale: .85, transformOrigin: "center center", ease: "none" }, 0);
    cable.from(".local-terminal", { x: -30, scale: .85, transformOrigin: "center center", ease: "none" }, 0);
    cable.from(".scene-wire", { strokeDashoffset: 1, ease: "none" }, 0);
  });
}
