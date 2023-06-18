

// 内存数字格式化
export const formatMemory = (memory: number): string => {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let size = memory;
    let unitIndex = 0;
    while (size >= 1024 && unitIndex < units.length - 1) {
        size /= 1024;
        unitIndex++;
    }
    return `${size.toFixed(2)} ${units[unitIndex]}`;
}


export const formatNumber = (num: number) => {
    if (num < 1000) {
        return num.toString();
    } else {
        const suffixes = ['', 'K', 'M', 'G', 'T', 'P', 'E', 'Z', 'Y'];
        let quotient = num;
        let suffixIndex = 0;
        while (quotient >= 1000 && suffixIndex < suffixes.length - 1) {
            quotient = Math.floor(quotient / 1000);
            suffixIndex++;
        }
        const suffix = suffixes[suffixIndex];
        const formattedQuotient = quotient.toString();
        if (suffix === '') {
            return formattedQuotient;
        } else {
            const formattedRemainder = Math.floor((num % 1000) / 100).toString();
            return `${formattedQuotient}.${formattedRemainder}${suffix}`;
        }
    }
}
export const formatDate = (date: Date) => {
    const hours = date.getHours();
    const minutes = date.getMinutes();
    const seconds = date.getSeconds();
    return hours + ':' + minutes + ':' + seconds;
}

export const  formatDate2 = (date:Date) => {
    //年份
    const Year : number = date.getFullYear(); 

    //月份下标是0-11
    const Months : any = ( date.getMonth() + 1 ) < 10  ?  '0' + (date.getMonth() + 1) : ( date.getMonth() + 1); 

    //具体的天数
    const Day : any = date.getDate() < 10 ? '0' + date.getDate() : date.getDate();

   //小时
   const Hours = date.getHours() < 10 ? '0' + date.getHours() : date.getHours();

   //分钟
   const Minutes = date.getMinutes() < 10 ? '0' + date.getMinutes() : date.getMinutes();

   //秒
   const Seconds = date.getSeconds() < 10 ? '0' + date.getSeconds() : date.getSeconds();

   //返回数据格式
   return Year + '-' + Months + '-' + Day + '-' + Hours + ':' + Minutes + ':' + Seconds; 
}