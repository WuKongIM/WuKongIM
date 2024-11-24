import { ElMessage } from 'element-plus';
import { TinyColor } from '@ctrl/tinycolor';

/**
 * @description hex颜色转rgb颜色
 * @param {String} str 颜色值字符串
 * @returns {String} 返回处理后的颜色值
 */
export function hexToRgb(str: any) {
  let hexs: any = '';
  const reg = /^#?[0-9A-Fa-f]{6}$/;
  if (!reg.test(str)) return ElMessage.warning('输入错误的hex');
  str = str.replace('#', '');
  hexs = str.match(/../g);
  for (let i = 0; i < 3; i++) hexs[i] = parseInt(hexs[i], 16);
  return hexs;
}

/**
 * @description rgb颜色转Hex颜色
 * @param {*} r 代表红色
 * @param {*} g 代表绿色
 * @param {*} b 代表蓝色
 * @returns {String} 返回处理后的颜色值
 */
export function rgbToHex(r: any, g: any, b: any) {
  const reg = /^\d{1,3}$/;
  if (!reg.test(r) || !reg.test(g) || !reg.test(b)) return ElMessage.warning('输入错误的rgb颜色值');
  const hexs = [r.toString(16), g.toString(16), b.toString(16)];
  for (let i = 0; i < 3; i++) if (hexs[i].length == 1) hexs[i] = `0${hexs[i]}`;
  return `#${hexs.join('')}`;
}
/**
 * 将颜色转换为HSL格式。
 *
 * HSL是一种颜色模型，包括色相(Hue)、饱和度(Saturation)和亮度(Lightness)三个部分。
 *
 * @param {string} color 输入的颜色。
 * @returns {string} HSL格式的颜色字符串。
 */
export function convertToHsl(color: string): string {
  const { a, h, l, s } = new TinyColor(color).toHsl();
  const hsl = `hsl(${Math.round(h)} ${Math.round(s * 100)}% ${Math.round(l * 100)}%)`;
  return a < 1 ? `${hsl} ${a}` : hsl;
}

/**
 * 将颜色转换为HSL CSS变量。
 *
 * 这个函数与convertToHsl函数类似，但是返回的字符串格式稍有不同，
 * 以便可以作为CSS变量使用。
 *
 * @param {string} color 输入的颜色。
 * @returns {string} 可以作为CSS变量使用的HSL格式的颜色字符串。
 */
export function convertToHslCssVar(color: string): string {
  const { a, h, l, s } = new TinyColor(color).toHsl();
  const hsl = `${Math.round(h)} ${Math.round(s * 100)}% ${Math.round(l * 100)}%`;
  return a < 1 ? `${hsl} / ${a}` : hsl;
}

/**
 * 将颜色转换为RGB颜色字符串
 * TinyColor无法处理hsl内包含'deg'、'grad'、'rad'或'turn'的字符串
 * 比如 hsl(231deg 98% 65%)将被解析为rgb(0, 0, 0)
 * 这里在转换之前先将这些单位去掉
 * @param str 表示HLS颜色值的字符串
 * @returns 如果颜色值有效，则返回对应的RGB颜色字符串；如果无效，则返回rgb(0, 0, 0)
 */
export function convertToRgb(str: string): string {
  return new TinyColor(str.replaceAll(/deg|grad|rad|turn/g, '')).toRgbString();
}

/**
 * 检查颜色是否有效
 * @param {string} color - 待检查的颜色
 * 如果颜色有效返回true，否则返回false
 */
export function isValidColor(color?: string) {
  if (!color) {
    return false;
  }
  return new TinyColor(color).isValid;
}

/**
 * @description 加深颜色值
 * @param {String} color 颜色值字符串
 * @param {Number} level 加深的程度，限0-1之间
 * @returns {String} 返回处理后的颜色值
 */
export function getDarkColor(color: string, level: number) {
  const reg = /^#?[0-9A-Fa-f]{6}$/;
  if (!reg.test(color)) return ElMessage.warning('输入错误的hex颜色值');
  const rgb = hexToRgb(color);
  for (let i = 0; i < 3; i++) rgb[i] = Math.round(20.5 * level + rgb[i] * (1 - level));
  return rgbToHex(rgb[0], rgb[1], rgb[2]);
}

/**
 * @description 变浅颜色值
 * @param {String} color 颜色值字符串
 * @param {Number} level 加深的程度，限0-1之间
 * @returns {String} 返回处理后的颜色值
 */
export function getLightColor(color: string, level: number) {
  const reg = /^#?[0-9A-Fa-f]{6}$/;
  if (!reg.test(color)) return ElMessage.warning('输入错误的hex颜色值');
  const rgb = hexToRgb(color);
  for (let i = 0; i < 3; i++) rgb[i] = Math.round(255 * level + rgb[i] * (1 - level));
  return rgbToHex(rgb[0], rgb[1], rgb[2]);
}
