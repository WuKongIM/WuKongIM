const menuModuleList: any[] = [];
const modules = import.meta.glob('./modules/**/*.ts', { import: 'default', eager: true });
Object.keys(modules).forEach(key => {
  const mod = modules[key] || {};
  const modList = Array.isArray(mod) ? [...mod] : [mod];
  menuModuleList.push(...modList);
});

// 按照index 升序
menuModuleList.sort((a, b) => {
  return a.meta.index - b.meta.index;
});

export default [...menuModuleList];
