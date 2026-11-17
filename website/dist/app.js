const stepContent = [
  { title: "邀请一次，记住彼此。", description: "一台生成邀请，另一台粘贴导入。两端通过设备公钥建立信任。", note: "5 分钟有效 · 仅限一次", next: "下一步：共享串口" },
  { title: "只开放你选中的串口。", description: "在共享端勾选目标端口，对端便能看到。试试右侧的共享开关。", note: "新接入的设备默认不共享", next: "下一步：本机连接" },
  { title: "工具照旧，串口换个位置。", description: "选择设备和串口，连接后用本机工具打开映射路径。", note: "也可在 Linkport 中直接监听与发送", next: "再看一次：配对机器" },
];

const stepTabs = [...document.querySelectorAll("[data-step]")];
const stepPanels = [...document.querySelectorAll(".step-panel")];
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
let currentStep = 0;

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

const pathContent = {
  direct: { description: "数据直接在两台设备间传输。", caption: "DEVICE ↔ DEVICE", label: "设备直连" },
  relay: { description: "中继只转发密文，串口内容由两端解密。", caption: "DEVICE ↔ RELAY ↔ DEVICE", label: "直连受限" },
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
  document.querySelector(".direct-label").textContent = pathContent[mode].label;
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
      scrollTrigger: { trigger: ".connection-scene", start: "top 96%", end: "bottom 30%", scrub: .5 },
    });
    cable.from(".hardware", { x: 35, scale: .85, transformOrigin: "center center", ease: "none" }, 0);
    cable.from(".local-terminal", { x: -30, scale: .85, transformOrigin: "center center", ease: "none" }, 0);
    cable.from(".scene-wire", { strokeDashoffset: 1, ease: "none" }, 0);
  });
}
