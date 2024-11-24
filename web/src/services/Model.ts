
export class Series {
    name?: string;
    type?: string = "line";
    data: Point[] = [];
}

export class Point {
    timestamp!: number
    value!: number
}

export const setSeries = (field: string, data: any, results: Array<Series>) => {
    const result = data[field]
    if (result && result.length > 0) {
        for (let index = 0; index < result.length; index++) {
            const label = result[index];
            var exist: any
            for (let index = 0; index < results.length; index++) {
                const element = results[index];
                if (element.name == label.label) {
                    exist = element
                    break
                }
            }
            if (exist) {
                exist.data.push({ timestamp: data.timestamp, value: label.value })
            } else {
                results.push({ name: label.label, data: [{ timestamp: data.timestamp, value: label.value }] })
            }
        }
    }
}